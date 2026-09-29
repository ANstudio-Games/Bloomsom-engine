package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
)

func setupTestDB(t *testing.T) (*storage.DB, *Service) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "auth_test.db")
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if _, err := db.Migrate(ctx, "custom"); err != nil {
		t.Fatalf("db.Migrate failed: %v", err)
	}

	cfg := config.AuthConfig{
		SessionTTL:       2 * time.Hour,
		AllowRegister:    true,
		MaxLoginAttempts: 3,
	}

	svc := NewService(db, cfg, nil)
	return db, svc
}

func TestArgon2(t *testing.T) {
	password := "mySuperSecretPassword123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("Hash format unexpected: %s", hash)
	}

	// Verify same password returns distinct hashes due to random salt
	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword 2 failed: %v", err)
	}
	if hash == hash2 {
		t.Errorf("expected different salts, got identical hashes")
	}

	// Positive verification
	ok, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword positive failed: %v", err)
	}
	if !ok {
		t.Errorf("VerifyPassword expected true, got false")
	}

	// Negative verification
	ok, err = VerifyPassword("wrongPassword123", hash)
	if err != nil {
		t.Fatalf("VerifyPassword negative returned error: %v", err)
	}
	if ok {
		t.Errorf("VerifyPassword expected false, got true")
	}

	// Invalid format handling
	malformed := []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA",        // wrong algo
		"$argon2id$v=99$m=65536,t=1,p=4$c2FsdA$aGFzaA",       // wrong version
		"$argon2id$v=19$m=0,t=1,p=4$c2FsdA$aGFzaA",           // invalid memory
		"$argon2id$v=19$invalid_params$c2FsdA$aGFzaA",        // bad params
		"$argon2id$v=19$m=65536,t=1,p=4$invalid_b64$aGFzaA",  // bad salt b64
		"$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$invalid_b64",  // bad hash b64
	}

	for _, m := range malformed {
		ok, err := VerifyPassword(password, m)
		if err == nil {
			t.Errorf("VerifyPassword(%q) expected error, got nil (ok=%v)", m, ok)
		}
		if ok {
			t.Errorf("VerifyPassword(%q) returned ok=true", m)
		}
	}
}

func TestTokenGeneration(t *testing.T) {
	rawToken, tokenHash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if len(rawToken) != 64 {
		t.Errorf("expected rawToken len 64, got %d", len(rawToken))
	}
	if len(tokenHash) != 64 {
		t.Errorf("expected tokenHash len 64, got %d", len(tokenHash))
	}

	if expected := HashToken(rawToken); expected != tokenHash {
		t.Errorf("HashToken mismatch: got %s, want %s", tokenHash, expected)
	}

	raw2, hash2, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken 2 failed: %v", err)
	}
	if rawToken == raw2 || tokenHash == hash2 {
		t.Errorf("GenerateToken produced duplicate tokens")
	}
}

func TestRegister(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	// 1. Valid user registration
	p, err := svc.Register(ctx, "Alice_01", "password123", "alice@example.com", "Alice Wonderland", "")
	if err != nil {
		t.Fatalf("Register valid user failed: %v", err)
	}
	if p.ID == 0 {
		t.Errorf("expected non-zero ID")
	}
	if p.Username != "alice_01" {
		t.Errorf("expected lowercase username alice_01, got %s", p.Username)
	}
	if p.Role != RolePlayer {
		t.Errorf("expected default role player, got %s", p.Role)
	}
	if p.Status != StatusActive {
		t.Errorf("expected status active, got %s", p.Status)
	}
	if p.Email == nil || *p.Email != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %v", p.Email)
	}
	if p.DisplayName != "Alice Wonderland" {
		t.Errorf("expected display name 'Alice Wonderland', got %s", p.DisplayName)
	}
	if p.CreatedAt.IsZero() {
		t.Errorf("expected non-zero CreatedAt")
	}

	// Verify register event recorded
	var eventCount int
	err = db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_events WHERE player_id = ? AND event = 'register'`, p.ID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("query auth_events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 register event, got %d", eventCount)
	}

	// 2. Username with invalid chars
	invalidUsernames := []string{
		"al",                                // too short (< 3)
		"this_username_is_way_too_long_for_registration_33", // > 32
		"alice with space",
		"alice@domain",
		"alice!",
		"alice-dash",
	}
	for _, uname := range invalidUsernames {
		_, err := svc.Register(ctx, uname, "password123", "", "", RolePlayer)
		if !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("Register with username %q expected ErrInvalidUsername, got %v", uname, err)
		}
	}

	// 3. Short password (< 6)
	shortPasswords := []string{"", "a", "12345"}
	for _, pw := range shortPasswords {
		_, err := svc.Register(ctx, "valid_user", pw, "", "", RolePlayer)
		if !errors.Is(err, ErrPasswordTooShort) {
			t.Errorf("Register with password %q expected ErrPasswordTooShort, got %v", pw, err)
		}
	}

	// 4. Duplicate username (case-insensitive check)
	_, err = svc.Register(ctx, "ALICE_01", "password456", "different@example.com", "", RolePlayer)
	if !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("Register duplicate username expected ErrUsernameTaken, got %v", err)
	}

	// 5. Duplicate email
	_, err = svc.Register(ctx, "bob_the_builder", "password123", "alice@example.com", "", RolePlayer)
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("Register duplicate email expected ErrEmailTaken, got %v", err)
	}

	// 6. Multiple registrations with empty email (should succeed since email is optional/NULL)
	p2, err := svc.Register(ctx, "user_no_email_1", "password123", "", "", RolePlayer)
	if err != nil {
		t.Fatalf("Register user without email failed: %v", err)
	}
	if p2.Email != nil {
		t.Errorf("expected nil email, got %v", *p2.Email)
	}

	p3, err := svc.Register(ctx, "user_no_email_2", "password123", "", "", RolePlayer)
	if err != nil {
		t.Fatalf("Register second user without email failed: %v", err)
	}
	if p3.Email != nil {
		t.Errorf("expected nil email, got %v", *p3.Email)
	}
}

func TestLogin(t *testing.T) {
	_, svc := setupTestDB(t)
	ctx := context.Background()

	// Register user
	_, err := svc.Register(ctx, "login_user", "correct_pass_123", "login@example.com", "Login User", RolePlayer)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// 1. Success login (case-insensitive username)
	sess, p, err := svc.Login(ctx, "LOGIN_USER", "correct_pass_123", "192.168.1.100", "v1.0.0")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if sess == nil || sess.Token == "" {
		t.Fatalf("Login expected session with raw token, got %v", sess)
	}
	if sess.PlayerID != p.ID {
		t.Errorf("Session.PlayerID (%d) != Player.ID (%d)", sess.PlayerID, p.ID)
	}
	if p.LastLoginAt == nil {
		t.Errorf("expected Player.LastLoginAt to be set")
	}

	// 2. Wrong password
	_, _, err = svc.Login(ctx, "login_user", "wrong_password", "192.168.1.100", "v1.0.0")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}

	// 3. Non-existent user
	_, _, err = svc.Login(ctx, "ghost_user", "any_password", "192.168.1.100", "v1.0.0")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRateLimiting(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "target_user", "super_secret_pw", "target@example.com", "", RolePlayer)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// MaxLoginAttempts is 3 in setupTestDB.
	// Fail 3 times:
	remoteAddr := "203.0.113.42"
	for i := 1; i <= 3; i++ {
		_, _, err := svc.Login(ctx, "target_user", "wrong_password", remoteAddr, "v1.0.0")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i, err)
		}
	}

	// 4th attempt with the CORRECT password should be rate limited immediately
	_, _, err = svc.Login(ctx, "target_user", "super_secret_pw", remoteAddr, "v1.0.0")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("attempt 4: expected ErrRateLimited, got %v", err)
	}

	// Verify rate_limited auth_event was recorded
	var rateLimitedCount int
	err = db.SQL().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM auth_events
		WHERE username = 'target_user' AND event = 'login_fail' AND reason = 'rate_limited'
	`).Scan(&rateLimitedCount)
	if err != nil {
		t.Fatalf("query auth_events: %v", err)
	}
	if rateLimitedCount != 1 {
		t.Errorf("expected 1 rate_limited event, got %d", rateLimitedCount)
	}
}

func TestBanAndUnban(t *testing.T) {
	_, svc := setupTestDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "banned_user", "password123", "ban@example.com", "", RolePlayer)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Log in to get active session
	sess, _, err := svc.Login(ctx, "banned_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Temporary ban for 150ms
	banDuration := 150 * time.Millisecond
	err = svc.BanPlayer(ctx, "banned_user", banDuration, "toxic behavior")
	if err != nil {
		t.Fatalf("BanPlayer failed: %v", err)
	}

	// Existing token must be rejected because BanPlayer revokes all active sessions
	_, err = svc.Authenticate(ctx, sess.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Errorf("Authenticate with token before ban expected ErrInvalidSession, got %v", err)
	}

	// Login during ban must fail with ErrPlayerBanned
	_, _, err = svc.Login(ctx, "banned_user", "password123", "127.0.0.1", "")
	if !errors.Is(err, ErrPlayerBanned) {
		t.Errorf("Login during ban expected ErrPlayerBanned, got %v", err)
	}

	// Wait for temporary ban to expire
	time.Sleep(200 * time.Millisecond)

	// Login after ban expiry must succeed and auto-unban the player
	sessAfterBan, pAfterBan, err := svc.Login(ctx, "banned_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login after ban expiry failed: %v", err)
	}
	if sessAfterBan.Token == "" {
		t.Errorf("expected valid session token after unban")
	}
	if pAfterBan.Status != StatusActive {
		t.Errorf("expected status active after ban expiry, got %s", pAfterBan.Status)
	}
	if pAfterBan.BannedUntil != nil {
		t.Errorf("expected nil BannedUntil after unban, got %v", pAfterBan.BannedUntil)
	}

	// Test permanent ban and explicit UnbanPlayer
	err = svc.BanPlayer(ctx, "banned_user", 0, "permanent ban")
	if err != nil {
		t.Fatalf("BanPlayer permanent failed: %v", err)
	}

	_, _, err = svc.Login(ctx, "banned_user", "password123", "127.0.0.1", "")
	if !errors.Is(err, ErrPlayerBanned) {
		t.Errorf("Login during permanent ban expected ErrPlayerBanned, got %v", err)
	}

	err = svc.UnbanPlayer(ctx, "banned_user")
	if err != nil {
		t.Fatalf("UnbanPlayer failed: %v", err)
	}

	_, _, err = svc.Login(ctx, "banned_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login after UnbanPlayer failed: %v", err)
	}
}

func TestTokenAuth(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "token_user", "password123", "token@example.com", "Token User", RolePlayer)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	sess, _, err := svc.Login(ctx, "token_user", "password123", "127.0.0.1", "1.0.0")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// 1. Valid token succeeds
	p, err := svc.Authenticate(ctx, sess.Token)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if p.Username != "token_user" {
		t.Errorf("expected username token_user, got %s", p.Username)
	}

	// 2. Revoked token fails
	err = svc.Logout(ctx, sess.Token)
	if err != nil {
		t.Fatalf("Logout failed: %v", err)
	}

	_, err = svc.Authenticate(ctx, sess.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Errorf("Authenticate revoked token expected ErrInvalidSession, got %v", err)
	}

	// Calling Logout again with already revoked token returns ErrInvalidSession
	err = svc.Logout(ctx, sess.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Errorf("second Logout expected ErrInvalidSession, got %v", err)
	}

	// 3. Expired token fails
	sess2, _, err := svc.Login(ctx, "token_user", "password123", "127.0.0.1", "1.0.0")
	if err != nil {
		t.Fatalf("Login 2 failed: %v", err)
	}

	// Set expires_at in the past
	hash2 := HashToken(sess2.Token)
	_, err = db.SQL().ExecContext(ctx, `
		UPDATE sessions SET expires_at = datetime('now', '-1 hour') WHERE token_hash = ?
	`, hash2)
	if err != nil {
		t.Fatalf("expire session in db: %v", err)
	}

	_, err = svc.Authenticate(ctx, sess2.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Errorf("Authenticate expired token expected ErrInvalidSession, got %v", err)
	}

	// 4. Invalid tokens
	for _, tok := range []string{"", "invalid-token-12345", "0000000000000000000000000000000000000000000000000000000000000000"} {
		_, err := svc.Authenticate(ctx, tok)
		if !errors.Is(err, ErrInvalidSession) {
			t.Errorf("Authenticate(%q) expected ErrInvalidSession, got %v", tok, err)
		}
	}
}

func TestSessionPurge(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "purge_user", "password123", "", "", RolePlayer)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Session 1: valid active session
	sess1, _, err := svc.Login(ctx, "purge_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login 1 failed: %v", err)
	}

	// Session 2: revoked session
	sess2, _, err := svc.Login(ctx, "purge_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login 2 failed: %v", err)
	}
	if err := svc.Logout(ctx, sess2.Token); err != nil {
		t.Fatalf("Logout sess2 failed: %v", err)
	}

	// Session 3: expired session
	sess3, _, err := svc.Login(ctx, "purge_user", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login 3 failed: %v", err)
	}
	hash3 := HashToken(sess3.Token)
	_, err = db.SQL().ExecContext(ctx, `
		UPDATE sessions SET expires_at = datetime('now', '-1 day') WHERE token_hash = ?
	`, hash3)
	if err != nil {
		t.Fatalf("expire sess3 failed: %v", err)
	}

	// Purge
	purged, err := svc.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired failed: %v", err)
	}
	if purged != 2 {
		t.Errorf("expected 2 purged sessions, got %d", purged)
	}

	// Session 1 should still be valid
	p, err := svc.Authenticate(ctx, sess1.Token)
	if err != nil {
		t.Fatalf("Authenticate sess1 failed after purge: %v", err)
	}
	if p.Username != "purge_user" {
		t.Errorf("expected username purge_user, got %s", p.Username)
	}

	// Verify only 1 session row remains in DB
	var totalSessions int
	err = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&totalSessions)
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if totalSessions != 1 {
		t.Errorf("expected 1 total session in DB, got %d", totalSessions)
	}
}

func TestAdminMethods(t *testing.T) {
	_, svc := setupTestDB(t)
	ctx := context.Background()

	// Register multiple players
	names := []string{"admin_alpha", "admin_beta", "admin_gamma"}
	for _, n := range names {
		_, err := svc.Register(ctx, n, "password123", n+"@example.com", strings.ToUpper(n), RolePlayer)
		if err != nil {
			t.Fatalf("Register %s failed: %v", n, err)
		}
	}

	// 1. ListPlayers pagination
	p1, err := svc.ListPlayers(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListPlayers limit 2, offset 0 failed: %v", err)
	}
	if len(p1) != 2 {
		t.Errorf("expected 2 players, got %d", len(p1))
	}
	if p1[0].Username != "admin_alpha" || p1[1].Username != "admin_beta" {
		t.Errorf("unexpected players in page 1: %+v", p1)
	}

	p2, err := svc.ListPlayers(ctx, 2, 2)
	if err != nil {
		t.Fatalf("ListPlayers limit 2, offset 2 failed: %v", err)
	}
	if len(p2) != 1 {
		t.Errorf("expected 1 player, got %d", len(p2))
	}
	if p2[0].Username != "admin_gamma" {
		t.Errorf("unexpected player in page 2: %+v", p2)
	}

	// 2. GetPlayer
	player, err := svc.GetPlayer(ctx, "ADMIN_BETA")
	if err != nil {
		t.Fatalf("GetPlayer failed: %v", err)
	}
	if player.Username != "admin_beta" {
		t.Errorf("expected username admin_beta, got %s", player.Username)
	}

	_, err = svc.GetPlayer(ctx, "non_existent_player")
	if !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("GetPlayer non-existent expected ErrPlayerNotFound, got %v", err)
	}

	// 3. ResetPassword
	// Log in to get active session before reset
	sess, _, err := svc.Login(ctx, "admin_alpha", "password123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login admin_alpha failed: %v", err)
	}

	// Reset with short password must fail
	err = svc.ResetPassword(ctx, "admin_alpha", "123")
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("ResetPassword short expected ErrPasswordTooShort, got %v", err)
	}

	// Reset non-existent player
	err = svc.ResetPassword(ctx, "non_existent", "newpassword123")
	if !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("ResetPassword non-existent expected ErrPlayerNotFound, got %v", err)
	}

	// Reset valid player
	err = svc.ResetPassword(ctx, "admin_alpha", "brand_new_password_123")
	if err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}

	// Prior session token must be revoked
	_, err = svc.Authenticate(ctx, sess.Token)
	if !errors.Is(err, ErrInvalidSession) {
		t.Errorf("Authenticate after password reset expected ErrInvalidSession, got %v", err)
	}

	// Old password must fail
	_, _, err = svc.Login(ctx, "admin_alpha", "password123", "127.0.0.1", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Login old password expected ErrInvalidCredentials, got %v", err)
	}

	// New password must succeed
	newSess, newPlayer, err := svc.Login(ctx, "admin_alpha", "brand_new_password_123", "127.0.0.1", "")
	if err != nil {
		t.Fatalf("Login new password failed: %v", err)
	}
	if newSess.Token == "" || newPlayer.Username != "admin_alpha" {
		t.Errorf("unexpected session/player after password reset")
	}
}
