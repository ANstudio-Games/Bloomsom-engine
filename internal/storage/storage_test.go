package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "nested", "dir", "game.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrate(t *testing.T, db *DB, preset string) []string {
	t.Helper()
	applied, err := db.Migrate(context.Background(), preset)
	if err != nil {
		t.Fatalf("Migrate(%q): %v", preset, err)
	}
	return applied
}

func tableNames(t *testing.T, db *DB) map[string]TableInfo {
	t.Helper()
	tables, err := db.Tables(context.Background())
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	m := make(map[string]TableInfo, len(tables))
	for _, ti := range tables {
		m[ti.Name] = ti
	}
	return m
}

func TestOpenCreatesFileAndSetsPragmas(t *testing.T) {
	db := openTest(t)
	if _, err := os.Stat(db.Path()); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	ctx := context.Background()
	var mode string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	var bt int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&bt); err != nil {
		t.Fatal(err)
	}
	if bt != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", bt)
	}
}

func TestMigrateTurnBasedIdempotent(t *testing.T) {
	db := openTest(t)
	got := migrate(t, db, "turn-based")
	want := []string{"core/0001_auth.sql", "core/0002_rooms.sql", "presets/turn-based/0001_turnbased.sql"}
	if !slices.Equal(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}

	again := migrate(t, db, "turn-based")
	if again == nil || len(again) != 0 {
		t.Fatalf("second Migrate applied = %#v, want empty non-nil slice", again)
	}

	ms, err := db.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
		if m.AppliedAt.IsZero() {
			t.Errorf("migration %s has zero AppliedAt", m.ID)
		}
	}
	if !slices.Equal(ids, want) {
		t.Errorf("AppliedMigrations = %v, want %v", ids, want)
	}

	tables := tableNames(t, db)
	for _, n := range []string{"players", "sessions", "auth_events", "game_rooms", "schema_migrations", "matches", "match_moves"} {
		ti, ok := tables[n]
		if !ok {
			t.Errorf("missing table %s", n)
		} else if !ti.Builtin {
			t.Errorf("table %s not marked builtin", n)
		}
	}
	if _, ok := tables["chat_messages"]; ok {
		t.Error("chat_messages should not exist for turn-based preset")
	}
	if ti := tables["schema_migrations"]; ti.Rows != 3 {
		t.Errorf("schema_migrations rows = %d, want 3", ti.Rows)
	}
}

func TestMigrateAddPresetLater(t *testing.T) {
	db := openTest(t)
	migrate(t, db, "turn-based")
	got := migrate(t, db, "lobby-chat")
	if want := []string{"presets/lobby-chat/0001_chat.sql"}; !slices.Equal(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}
	if _, ok := tableNames(t, db)["chat_messages"]; !ok {
		t.Error("chat_messages missing")
	}
}

func TestMigrateOtherPresets(t *testing.T) {
	for preset, wantTables := range map[string][]string{
		"realtime-action": {"player_states", "match_results"},
		"custom":          {"players", "game_rooms"},
	} {
		t.Run(preset, func(t *testing.T) {
			db := openTest(t)
			migrate(t, db, preset)
			tables := tableNames(t, db)
			for _, n := range wantTables {
				if _, ok := tables[n]; !ok {
					t.Errorf("missing table %s", n)
				}
			}
		})
	}
}

func TestMigrateUnknownPreset(t *testing.T) {
	db := openTest(t)
	applied, err := db.Migrate(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want none", applied)
	}
	if _, ok := tableNames(t, db)["players"]; ok {
		t.Error("players table created despite unknown preset")
	}
}

func TestAuthSchemaConstraints(t *testing.T) {
	db := openTest(t)
	migrate(t, db, "custom")
	ctx := context.Background()
	sqlDB := db.SQL()

	res, err := sqlDB.ExecContext(ctx,
		`INSERT INTO players (username, password_hash) VALUES (?, ?)`, "alice", "hash")
	if err != nil {
		t.Fatalf("insert player: %v", err)
	}
	pid, _ := res.LastInsertId()

	var role, status string
	if err := sqlDB.QueryRowContext(ctx, `SELECT role, status FROM players WHERE id = ?`, pid).Scan(&role, &status); err != nil {
		t.Fatal(err)
	}
	if role != "player" || status != "active" {
		t.Errorf("defaults = %q/%q, want player/active", role, status)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO players (username, password_hash) VALUES (?, ?)`, "alice", "h2"); err == nil {
		t.Error("duplicate username accepted")
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO players (username, password_hash, role) VALUES (?, ?, ?)`, "bob", "h", "superuser"); err == nil {
		t.Error("role superuser accepted")
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO auth_events (event) VALUES (?)`, "hacked"); err == nil {
		t.Error("invalid auth event accepted")
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO sessions (player_id, token_hash, expires_at) VALUES (?, ?, datetime('now', '+1 day'))`,
		pid, "tok1"); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO auth_events (player_id, username, event) VALUES (?, ?, ?)`, pid, "alice", "login_ok"); err != nil {
		t.Fatalf("insert auth_event: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO sessions (player_id, token_hash, expires_at) VALUES (?, ?, datetime('now'))`,
		9999, "tok2"); err == nil {
		t.Error("session with unknown player accepted (foreign keys off?)")
	}

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM players WHERE id = ?`, pid); err != nil {
		t.Fatalf("delete player: %v", err)
	}
	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sessions after cascade = %d, want 0", n)
	}
	var nullCount int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_events WHERE player_id IS NULL AND username = 'alice'`).Scan(&nullCount); err != nil {
		t.Fatal(err)
	}
	if nullCount != 1 {
		t.Errorf("auth_events SET NULL rows = %d, want 1", nullCount)
	}
}

func TestValidIdent(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"A", false},
		{"1abc", false},
		{"ok_name", true},
		{"_x9", true},
		{"has-dash", false},
		{"has space", false},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
		{`x"; DROP TABLE players;--`, false},
	}
	for _, tt := range tests {
		if got := ValidIdent(tt.in); got != tt.want {
			t.Errorf("ValidIdent(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseColumns(t *testing.T) {
	cols, err := ParseColumns([]string{"score:integer", "label:text:notnull", "ratio:Real", "raw:BLOB"})
	if err != nil {
		t.Fatalf("ParseColumns: %v", err)
	}
	want := []Column{
		{Name: "score", Type: "INTEGER"},
		{Name: "label", Type: "TEXT", NotNull: true},
		{Name: "ratio", Type: "REAL"},
		{Name: "raw", Type: "BLOB"},
	}
	if !slices.Equal(cols, want) {
		t.Errorf("cols = %+v, want %+v", cols, want)
	}

	bad := map[string][]string{
		"bad type":       {"score:varchar"},
		"missing type":   {"score"},
		"duplicate":      {"a:text", "a:integer"},
		"reserved id":    {"id:integer"},
		"reserved ts":    {"created_at:text"},
		"bad modifier":   {"a:text:unique"},
		"bad name":       {"Bad:text"},
		"injection type": {"a:TEXT); DROP TABLE players;--"},
		"empty":          {},
	}
	for name, specs := range bad {
		if _, err := ParseColumns(specs); err == nil {
			t.Errorf("%s: ParseColumns(%q) expected error", name, specs)
		}
	}
}

func TestCreateCustomTable(t *testing.T) {
	db := openTest(t)
	migrate(t, db, "custom")
	ctx := context.Background()
	cols := []Column{{Name: "item", Type: "text", NotNull: true}, {Name: "qty", Type: "INTEGER"}}

	full, err := db.CreateCustomTable(ctx, "inventory", cols)
	if err != nil {
		t.Fatalf("CreateCustomTable: %v", err)
	}
	if full != "custom_inventory" {
		t.Errorf("name = %q, want custom_inventory", full)
	}
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO custom_inventory (item, qty) VALUES (?, ?)`, "sword", 1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO custom_inventory (qty) VALUES (?)`, 1); err == nil {
		t.Error("NOT NULL on item not enforced")
	}
	ti, ok := tableNames(t, db)["custom_inventory"]
	if !ok || ti.Builtin || ti.Rows != 1 {
		t.Errorf("custom_inventory info = %+v (found=%v), want Builtin=false Rows=1", ti, ok)
	}

	if _, err := db.CreateCustomTable(ctx, "inventory", cols); !errors.Is(err, ErrTableExists) {
		t.Errorf("second create err = %v, want ErrTableExists", err)
	}

	// A builtin name gets prefixed and never touches the builtin table.
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO players (username, password_hash) VALUES (?, ?)`, "alice", "h"); err != nil {
		t.Fatal(err)
	}
	full, err = db.CreateCustomTable(ctx, "players", []Column{{Name: "x", Type: "TEXT"}})
	if err != nil || full != "custom_players" {
		t.Fatalf("CreateCustomTable(players) = %q, %v", full, err)
	}
	if ti := tableNames(t, db)["players"]; !ti.Builtin || ti.Rows != 1 {
		t.Errorf("builtin players changed: %+v", ti)
	}

	for _, name := range []string{`inv"; DROP TABLE players;--`, "custom_x", "", "Inv"} {
		if _, err := db.CreateCustomTable(ctx, name, cols); err == nil {
			t.Errorf("CreateCustomTable(%q) expected error", name)
		}
	}
	if _, err := db.CreateCustomTable(ctx, "evil", []Column{{Name: `a" TEXT); DROP TABLE players;--`, Type: "TEXT"}}); err == nil {
		t.Error("malicious column name accepted")
	}
	if _, err := db.CreateCustomTable(ctx, "evil2", []Column{{Name: "a", Type: "TEXT); DROP TABLE players;--"}}); err == nil {
		t.Error("malicious column type accepted")
	}
	if _, err := db.CreateCustomTable(ctx, "empty", nil); err == nil {
		t.Error("no columns accepted")
	}
	if _, ok := tableNames(t, db)["players"]; !ok {
		t.Fatal("players table was dropped")
	}
}
