// Package storage wraps the embedded SQLite database: opening it with the
// required pragmas, applying versioned migrations, and managing custom tables.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations
var migrationsFS embed.FS

// customPrefix is prepended to every table created via CreateCustomTable.
const customPrefix = "custom_"

// ErrTableExists is returned (wrapped) when a custom table already exists.
var ErrTableExists = errors.New("table already exists")

// presets maps a known preset name to its migrations directory (empty = none).
var presets = map[string]string{
	"realtime-action": "presets/realtime-action",
	"turn-based":      "presets/turn-based",
	"lobby-chat":      "presets/lobby-chat",
	"custom":          "",
}

// allowedTypes is the whitelist of column types for custom tables.
var allowedTypes = map[string]bool{
	"INTEGER": true,
	"REAL":    true,
	"TEXT":    true,
	"BLOB":    true,
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// DB wraps the SQLite handle.
type DB struct {
	sql  *sql.DB
	path string
}

// Migration is an applied migration record.
type Migration struct {
	ID        string
	AppliedAt time.Time
}

// TableInfo describes a user table and its row count.
type TableInfo struct {
	Name    string
	Rows    int64
	Builtin bool
}

// Column describes a custom table column.
type Column struct {
	Name    string
	Type    string
	NotNull bool
}

// Open opens (creating if needed) the SQLite file at path, creating parent
// directories. Pragmas are set through the DSN so they apply to every pooled
// connection.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("storage: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create dir %q: %w", dir, err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", path, err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: ping %q: %w", path, err)
	}
	return &DB{sql: sqlDB, path: path}, nil
}

// Close closes the underlying handle.
func (db *DB) Close() error { return db.sql.Close() }

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// SQL returns the raw *sql.DB handle.
func (db *DB) SQL() *sql.DB { return db.sql }

// listMigrations returns the migration IDs in dir sorted by filename.
func listMigrations(dir string) ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, path.Join("migrations", dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		ids = append(ids, path.Join(dir, e.Name()))
	}
	sort.Strings(ids)
	return ids, nil
}

// Migrate applies pending core migrations, then pending migrations for preset.
// Each migration runs in its own transaction with its schema_migrations row.
func (db *DB) Migrate(ctx context.Context, preset string) (applied []string, err error) {
	presetDir, ok := presets[preset]
	if !ok {
		return nil, fmt.Errorf("storage: unknown preset %q", preset)
	}

	ids, err := listMigrations("core")
	if err != nil {
		return nil, fmt.Errorf("storage: list core migrations: %w", err)
	}
	if presetDir != "" {
		pids, err := listMigrations(presetDir)
		if err != nil {
			return nil, fmt.Errorf("storage: list preset migrations: %w", err)
		}
		ids = append(ids, pids...)
	}

	if _, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		id         TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return nil, fmt.Errorf("storage: create schema_migrations: %w", err)
	}

	done := map[string]bool{}
	existing, err := db.AppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range existing {
		done[m.ID] = true
	}

	applied = []string{}
	for _, id := range ids {
		if done[id] {
			continue
		}
		body, err := migrationsFS.ReadFile(path.Join("migrations", id))
		if err != nil {
			return applied, fmt.Errorf("storage: read migration %s: %w", id, err)
		}
		if err := db.applyOne(ctx, id, string(body)); err != nil {
			return applied, err
		}
		applied = append(applied, id)
	}
	return applied, nil
}

func (db *DB) applyOne(ctx context.Context, id, body string) (err error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin migration %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("storage: apply migration %s: %w", id, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)`,
		id, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		return fmt.Errorf("storage: record migration %s: %w", id, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit migration %s: %w", id, err)
	}
	return nil
}

// AppliedMigrations returns all applied migration IDs in apply order.
func (db *DB) AppliedMigrations(ctx context.Context) ([]Migration, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT id, applied_at FROM schema_migrations ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("storage: query migrations: %w", err)
	}
	defer rows.Close()

	var out []Migration
	for rows.Next() {
		var (
			m   Migration
			raw any
		)
		if err := rows.Scan(&m.ID, &raw); err != nil {
			return nil, fmt.Errorf("storage: scan migration: %w", err)
		}
		m.AppliedAt, err = toTime(raw)
		if err != nil {
			return nil, fmt.Errorf("storage: migration %s applied_at: %w", m.ID, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// toTime converts a DATETIME column value to UTC time.Time. modernc returns
// time.Time for DATETIME-declared columns, but text is handled as a fallback.
func toTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), nil
	case string:
		return parseTimeString(t)
	case []byte:
		return parseTimeString(string(t))
	default:
		return time.Time{}, fmt.Errorf("unexpected type %T", v)
	}
}

func parseTimeString(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

// quoteIdent double-quotes an identifier, escaping embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// Tables lists user tables sorted by name with row counts.
func (db *DB) Tables(ctx context.Context) ([]TableInfo, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT name FROM sqlite_master
		 WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		 ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("storage: list tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("storage: scan table name: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]TableInfo, 0, len(names))
	for _, n := range names {
		var count int64
		// Name comes from sqlite_master and is quoted; identifiers cannot be bound.
		if err := db.sql.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+quoteIdent(n)).Scan(&count); err != nil {
			return nil, fmt.Errorf("storage: count %s: %w", n, err)
		}
		out = append(out, TableInfo{
			Name:    n,
			Rows:    count,
			Builtin: !strings.HasPrefix(n, customPrefix),
		})
	}
	return out, nil
}

// ValidIdent reports whether s matches ^[a-z_][a-z0-9_]{0,62}$.
func ValidIdent(s string) bool { return identRe.MatchString(s) }

// validateColumns checks names, reserved words, duplicates and types, and
// returns a normalized copy with uppercase types.
func validateColumns(cols []Column) ([]Column, error) {
	if len(cols) == 0 {
		return nil, errors.New("storage: at least one column is required")
	}
	seen := make(map[string]bool, len(cols))
	out := make([]Column, 0, len(cols))
	for _, c := range cols {
		if !ValidIdent(c.Name) {
			return nil, fmt.Errorf("storage: invalid column name %q (must match %s)", c.Name, identRe)
		}
		if c.Name == "id" || c.Name == "created_at" {
			return nil, fmt.Errorf("storage: column name %q is reserved", c.Name)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("storage: duplicate column %q", c.Name)
		}
		seen[c.Name] = true
		typ := strings.ToUpper(strings.TrimSpace(c.Type))
		if !allowedTypes[typ] {
			return nil, fmt.Errorf("storage: invalid type %q for column %q (allowed: INTEGER, REAL, TEXT, BLOB)", c.Type, c.Name)
		}
		out = append(out, Column{Name: c.Name, Type: typ, NotNull: c.NotNull})
	}
	return out, nil
}

// ParseColumns parses CLI specs "name:TYPE" or "name:TYPE:notnull".
func ParseColumns(specs []string) ([]Column, error) {
	cols := make([]Column, 0, len(specs))
	for _, spec := range specs {
		parts := strings.Split(spec, ":")
		if len(parts) < 2 || len(parts) > 3 || parts[1] == "" {
			return nil, fmt.Errorf("storage: invalid column spec %q (want name:TYPE[:notnull])", spec)
		}
		c := Column{Name: parts[0], Type: parts[1]}
		if len(parts) == 3 {
			if !strings.EqualFold(parts[2], "notnull") {
				return nil, fmt.Errorf("storage: invalid column modifier %q in %q (only notnull)", parts[2], spec)
			}
			c.NotNull = true
		}
		cols = append(cols, c)
	}
	return validateColumns(cols)
}

// CreateCustomTable creates table "custom_<name>" with an implicit id primary
// key, the given columns, and created_at. DDL is built only from validated
// identifiers and whitelisted types (identifiers cannot be parameter-bound).
func (db *DB) CreateCustomTable(ctx context.Context, name string, cols []Column) (string, error) {
	if strings.HasPrefix(name, customPrefix) {
		return "", fmt.Errorf("storage: table name %q must not include the %q prefix; it is added automatically", name, customPrefix)
	}
	if !ValidIdent(name) {
		return "", fmt.Errorf("storage: invalid table name %q (must match %s)", name, identRe)
	}
	full := customPrefix + name
	if !ValidIdent(full) {
		return "", fmt.Errorf("storage: table name %q too long after adding %q prefix", name, customPrefix)
	}
	norm, err := validateColumns(cols)
	if err != nil {
		return "", err
	}

	var exists int
	if err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, full).Scan(&exists); err != nil {
		return "", fmt.Errorf("storage: check table %s: %w", full, err)
	}
	if exists > 0 {
		return "", fmt.Errorf("storage: %s: %w", full, ErrTableExists)
	}

	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(quoteIdent(full))
	b.WriteString(" (\"id\" INTEGER PRIMARY KEY")
	for _, c := range norm {
		b.WriteString(", ")
		b.WriteString(quoteIdent(c.Name))
		b.WriteString(" ")
		b.WriteString(c.Type)
		if c.NotNull {
			b.WriteString(" NOT NULL")
		}
	}
	b.WriteString(", \"created_at\" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)")

	if _, err := db.sql.ExecContext(ctx, b.String()); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("storage: %s: %w", full, ErrTableExists)
		}
		return "", fmt.Errorf("storage: create table %s: %w", full, err)
	}
	return full, nil
}
