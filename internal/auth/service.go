package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
)

// Sentinel errors returned by the auth service.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrPlayerBanned       = errors.New("player account is banned")
	ErrRateLimited        = errors.New("too many failed login attempts, please try again later")
	ErrInvalidSession     = errors.New("invalid or expired session token")
	ErrUsernameTaken      = errors.New("username is already taken")
	ErrEmailTaken         = errors.New("email is already registered")
	ErrPlayerNotFound     = errors.New("player not found")
	ErrInvalidUsername    = errors.New("invalid username: must be 3-32 characters of [a-zA-Z0-9_]")
	ErrPasswordTooShort   = errors.New("password must be at least 6 characters")
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// Service provides authentication, session management, and player administration.
type Service struct {
	db  *storage.DB
	cfg config.AuthConfig
	log *slog.Logger
}

// NewService constructs a new auth Service.
func NewService(db *storage.DB, cfg config.AuthConfig, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		db:  db,
		cfg: cfg,
		log: log,
	}
}

// Register validates player inputs, ensures username/email uniqueness, hashes the password,
// creates the player account, records the register auth event, and returns the Player.
func (s *Service) Register(ctx context.Context, username, password, email, displayName string, role Role) (*Player, error) {
	if !usernameRegex.MatchString(username) {
		return nil, ErrInvalidUsername
	}
	if len(password) < 6 {
		return nil, ErrPasswordTooShort
	}

	username = strings.ToLower(username)

	if role == "" {
		role = RolePlayer
	} else if role != RolePlayer && role != RoleAdmin {
		return nil, fmt.Errorf("invalid role %q (must be %q or %q)", role, RolePlayer, RoleAdmin)
	}

	var emailPtr *string
	trimmedEmail := strings.TrimSpace(email)
	if trimmedEmail != "" {
		emailPtr = &trimmedEmail
	}

	// Check duplicates before inserting
	var count int
	err := s.db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM players WHERE username = ?`, username).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("auth: check username duplicate: %w", err)
	}
	if count > 0 {
		return nil, ErrUsernameTaken
	}

	if emailPtr != nil {
		err = s.db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM players WHERE email = ?`, *emailPtr).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("auth: check email duplicate: %w", err)
		}
		if count > 0 {
			return nil, ErrEmailTaken
		}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}

	res, err := s.db.SQL().ExecContext(ctx, `
		INSERT INTO players (username, email, password_hash, display_name, role, status)
		VALUES (?, ?, ?, ?, ?, ?)
	`, username, emailPtr, hash, displayName, role, StatusActive)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "players.username") || (strings.Contains(errStr, "UNIQUE constraint failed") && strings.Contains(errStr, "username")) {
			return nil, ErrUsernameTaken
		}
		if strings.Contains(errStr, "players.email") || (strings.Contains(errStr, "UNIQUE constraint failed") && strings.Contains(errStr, "email")) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("auth: insert player: %w", err)
	}

	playerID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("auth: get player id: %w", err)
	}

	_, err = s.db.SQL().ExecContext(ctx, `
		INSERT INTO auth_events (player_id, username, event)
		VALUES (?, ?, 'register')
	`, playerID, username)
	if err != nil {
		s.log.ErrorContext(ctx, "auth: failed to insert register event", "player_id", playerID, "error", err)
	}

	return s.getPlayerByID(ctx, playerID)
}

// Login checks rate limits, validates credentials and ban status, creates a new session,
// updates last_login_at, records auth_events, and returns the Session and Player.
func (s *Service) Login(ctx context.Context, username, password, remoteAddr, clientVer string) (*Session, *Player, error) {
	username = strings.ToLower(username)

	// Check rate limiting in auth_events for login_fail in the last 15 minutes.
	var failCount int
	var countErr error
	if remoteAddr != "" {
		countErr = s.db.SQL().QueryRowContext(ctx, `
			SELECT COUNT(*) FROM auth_events
			WHERE (username = ? OR remote_addr = ?)
			  AND event = 'login_fail'
			  AND created_at > datetime('now', '-15 minutes')
		`, username, remoteAddr).Scan(&failCount)
	} else {
		countErr = s.db.SQL().QueryRowContext(ctx, `
			SELECT COUNT(*) FROM auth_events
			WHERE username = ?
			  AND event = 'login_fail'
			  AND created_at > datetime('now', '-15 minutes')
		`, username).Scan(&failCount)
	}
	if countErr != nil {
		return nil, nil, fmt.Errorf("auth: check rate limit: %w", countErr)
	}

	if s.cfg.MaxLoginAttempts > 0 && failCount >= s.cfg.MaxLoginAttempts {
		_, _ = s.db.SQL().ExecContext(ctx, `
			INSERT INTO auth_events (username, event, reason, remote_addr)
			VALUES (?, 'login_fail', 'rate_limited', ?)
		`, username, nullIfEmpty(remoteAddr))
		return nil, nil, ErrRateLimited
	}

	// Query player by username
	var (
		p            Player
		passwordHash string
		email        sql.NullString
		displayName  sql.NullString
		role         string
		status       string
		bannedUntil  any
		banReason    sql.NullString
		createdAt    any
		updatedAt    any
		lastLoginAt  any
	)

	err := s.db.SQL().QueryRowContext(ctx, `
		SELECT id, username, email, password_hash, display_name, role, status,
		       banned_until, ban_reason, created_at, updated_at, last_login_at
		FROM players
		WHERE username = ?
	`, username).Scan(
		&p.ID,
		&p.Username,
		&email,
		&passwordHash,
		&displayName,
		&role,
		&status,
		&bannedUntil,
		&banReason,
		&createdAt,
		&updatedAt,
		&lastLoginAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = s.db.SQL().ExecContext(ctx, `
				INSERT INTO auth_events (username, event, reason, remote_addr)
				VALUES (?, 'login_fail', 'bad_credentials', ?)
			`, username, nullIfEmpty(remoteAddr))
			return nil, nil, ErrInvalidCredentials
		}
		return nil, nil, fmt.Errorf("auth: query player for login: %w", err)
	}

	if email.Valid {
		p.Email = &email.String
	}
	if displayName.Valid {
		p.DisplayName = displayName.String
	}
	p.Role = Role(role)
	p.Status = Status(status)
	if bannedUntil != nil {
		if t, err := toTime(bannedUntil); err == nil {
			p.BannedUntil = &t
		}
	}
	if banReason.Valid {
		p.BanReason = &banReason.String
	}
	if createdAt != nil {
		p.CreatedAt, _ = toTime(createdAt)
	}
	if updatedAt != nil {
		p.UpdatedAt, _ = toTime(updatedAt)
	}
	if lastLoginAt != nil {
		if t, err := toTime(lastLoginAt); err == nil {
			p.LastLoginAt = &t
		}
	}

	// Check player ban status
	if p.Status == StatusBanned {
		if p.BannedUntil != nil && time.Now().UTC().After(*p.BannedUntil) {
			// Ban has expired; automatically unban the player.
			_, err := s.db.SQL().ExecContext(ctx, `
				UPDATE players
				SET status = 'active', banned_until = NULL, ban_reason = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, p.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("auth: auto unban: %w", err)
			}
			p.Status = StatusActive
			p.BannedUntil = nil
			p.BanReason = nil
		} else {
			_, _ = s.db.SQL().ExecContext(ctx, `
				INSERT INTO auth_events (player_id, username, event, reason, remote_addr)
				VALUES (?, ?, 'login_fail', 'banned', ?)
			`, p.ID, p.Username, nullIfEmpty(remoteAddr))
			return nil, nil, ErrPlayerBanned
		}
	}

	// Verify argon2id password
	valid, err := VerifyPassword(password, passwordHash)
	if err != nil || !valid {
		_, _ = s.db.SQL().ExecContext(ctx, `
			INSERT INTO auth_events (player_id, username, event, reason, remote_addr)
			VALUES (?, ?, 'login_fail', 'bad_credentials', ?)
		`, p.ID, p.Username, nullIfEmpty(remoteAddr))
		return nil, nil, ErrInvalidCredentials
	}

	// Generate session token
	rawToken, tokenHash, err := GenerateToken()
	if err != nil {
		return nil, nil, fmt.Errorf("auth: generate token: %w", err)
	}

	ttl := s.cfg.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	expiresAt := time.Now().UTC().Add(ttl)
	expiresAtStr := expiresAt.Format("2006-01-02 15:04:05")

	res, err := s.db.SQL().ExecContext(ctx, `
		INSERT INTO sessions (player_id, token_hash, client_version, remote_addr, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, p.ID, tokenHash, nullIfEmpty(clientVer), nullIfEmpty(remoteAddr), expiresAtStr)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: insert session: %w", err)
	}

	sessionID, err := res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("auth: get session id: %w", err)
	}

	// Update players.last_login_at = CURRENT_TIMESTAMP
	_, err = s.db.SQL().ExecContext(ctx, `
		UPDATE players SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?
	`, p.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "auth: failed to update last_login_at", "player_id", p.ID, "error", err)
	}
	now := time.Now().UTC()
	p.LastLoginAt = &now

	// Insert auth_events(player_id, username, event, remote_addr) with event='login_ok'
	_, err = s.db.SQL().ExecContext(ctx, `
		INSERT INTO auth_events (player_id, username, event, remote_addr)
		VALUES (?, ?, 'login_ok', ?)
	`, p.ID, p.Username, nullIfEmpty(remoteAddr))
	if err != nil {
		s.log.ErrorContext(ctx, "auth: failed to insert login_ok event", "player_id", p.ID, "error", err)
	}

	session := &Session{
		ID:        sessionID,
		PlayerID:  p.ID,
		Token:     rawToken,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	return session, &p, nil
}

// Authenticate verifies the raw token, checks if the session and player account are valid,
// updates the session last_seen_at timestamp, and returns the Player.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (*Player, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, ErrInvalidSession
	}

	tokenHash := HashToken(rawToken)

	var (
		p           Player
		email       sql.NullString
		displayName sql.NullString
		role        string
		status      string
		bannedUntil any
		banReason   sql.NullString
		createdAt   any
		updatedAt   any
		lastLoginAt any
	)

	err := s.db.SQL().QueryRowContext(ctx, `
		SELECT p.id, p.username, p.email, p.display_name, p.role, p.status,
		       p.banned_until, p.ban_reason, p.created_at, p.updated_at, p.last_login_at
		FROM sessions s
		JOIN players p ON s.player_id = p.id
		WHERE s.token_hash = ?
		  AND s.revoked_at IS NULL
		  AND s.expires_at > CURRENT_TIMESTAMP
	`, tokenHash).Scan(
		&p.ID,
		&p.Username,
		&email,
		&displayName,
		&role,
		&status,
		&bannedUntil,
		&banReason,
		&createdAt,
		&updatedAt,
		&lastLoginAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidSession
		}
		return nil, fmt.Errorf("auth: authenticate session: %w", err)
	}

	if email.Valid {
		p.Email = &email.String
	}
	if displayName.Valid {
		p.DisplayName = displayName.String
	}
	p.Role = Role(role)
	p.Status = Status(status)
	if bannedUntil != nil {
		if t, err := toTime(bannedUntil); err == nil {
			p.BannedUntil = &t
		}
	}
	if banReason.Valid {
		p.BanReason = &banReason.String
	}
	if createdAt != nil {
		p.CreatedAt, _ = toTime(createdAt)
	}
	if updatedAt != nil {
		p.UpdatedAt, _ = toTime(updatedAt)
	}
	if lastLoginAt != nil {
		if t, err := toTime(lastLoginAt); err == nil {
			p.LastLoginAt = &t
		}
	}

	if p.Status == StatusBanned {
		if p.BannedUntil != nil && time.Now().UTC().After(*p.BannedUntil) {
			_, err := s.db.SQL().ExecContext(ctx, `
				UPDATE players
				SET status = 'active', banned_until = NULL, ban_reason = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, p.ID)
			if err != nil {
				return nil, fmt.Errorf("auth: auto unban: %w", err)
			}
			p.Status = StatusActive
			p.BannedUntil = nil
			p.BanReason = nil
		} else {
			return nil, ErrPlayerBanned
		}
	}

	_, err = s.db.SQL().ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE token_hash = ?
	`, tokenHash)
	if err != nil {
		s.log.ErrorContext(ctx, "auth: update session last_seen_at", "error", err)
	}

	return &p, nil
}

// Logout revokes the session matching rawToken and records the logout auth_event.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if strings.TrimSpace(rawToken) == "" {
		return ErrInvalidSession
	}

	tokenHash := HashToken(rawToken)

	var playerID sql.NullInt64
	var username sql.NullString
	var remoteAddr sql.NullString
	err := s.db.SQL().QueryRowContext(ctx, `
		SELECT s.player_id, p.username, s.remote_addr
		FROM sessions s
		LEFT JOIN players p ON s.player_id = p.id
		WHERE s.token_hash = ?
	`, tokenHash).Scan(&playerID, &username, &remoteAddr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidSession
		}
		return fmt.Errorf("auth: query session for logout: %w", err)
	}

	res, err := s.db.SQL().ExecContext(ctx, `
		UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP WHERE token_hash = ? AND revoked_at IS NULL
	`, tokenHash)
	if err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: rows affected: %w", err)
	}
	if affected == 0 {
		return ErrInvalidSession
	}

	var pid any
	if playerID.Valid {
		pid = playerID.Int64
	}
	var uname any
	if username.Valid {
		uname = username.String
	}
	var raddr any
	if remoteAddr.Valid {
		raddr = remoteAddr.String
	}

	_, err = s.db.SQL().ExecContext(ctx, `
		INSERT INTO auth_events (player_id, username, event, remote_addr)
		VALUES (?, ?, 'logout', ?)
	`, pid, uname, raddr)
	if err != nil {
		s.log.ErrorContext(ctx, "auth: insert logout event", "error", err)
	}

	return nil
}

// PurgeExpired deletes sessions that have expired or been revoked, returning the deleted count.
func (s *Service) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.SQL().ExecContext(ctx, `
		DELETE FROM sessions
		WHERE expires_at <= CURRENT_TIMESTAMP OR revoked_at IS NOT NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("auth: purge expired sessions: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("auth: purge rows affected: %w", err)
	}
	return count, nil
}

// ListPlayers returns a page of players ordered by id.
func (s *Service) ListPlayers(ctx context.Context, limit, offset int) ([]Player, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT id, username, email, display_name, role, status,
		       banned_until, ban_reason, created_at, updated_at, last_login_at
		FROM players
		ORDER BY id ASC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("auth: list players: %w", err)
	}
	defer rows.Close()

	players := make([]Player, 0)
	for rows.Next() {
		p, err := scanPlayerRow(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: scan player: %w", err)
		}
		players = append(players, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list players rows: %w", err)
	}
	return players, nil
}

// GetPlayer retrieves a player by username.
func (s *Service) GetPlayer(ctx context.Context, username string) (*Player, error) {
	username = strings.ToLower(username)
	row := s.db.SQL().QueryRowContext(ctx, `
		SELECT id, username, email, display_name, role, status,
		       banned_until, ban_reason, created_at, updated_at, last_login_at
		FROM players
		WHERE username = ?
	`, username)

	p, err := scanPlayerRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPlayerNotFound
		}
		return nil, fmt.Errorf("auth: get player: %w", err)
	}
	return p, nil
}

// BanPlayer sets status='banned', records duration and reason, revokes active sessions, and inserts an auth_event.
func (s *Service) BanPlayer(ctx context.Context, username string, duration time.Duration, reason string) error {
	username = strings.ToLower(username)

	player, err := s.GetPlayer(ctx, username)
	if err != nil {
		return err
	}

	var bannedUntil any
	if duration > 0 {
		bannedUntil = time.Now().UTC().Add(duration).Format("2006-01-02 15:04:05.999999999")
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin ban tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		UPDATE players
		SET status = 'banned', ban_reason = ?, banned_until = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, nullIfEmpty(reason), bannedUntil, player.ID)
	if err != nil {
		return fmt.Errorf("auth: update player ban: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE player_id = ? AND revoked_at IS NULL
	`, player.ID)
	if err != nil {
		return fmt.Errorf("auth: revoke player sessions: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO auth_events (player_id, username, event, reason)
		VALUES (?, ?, 'banned', ?)
	`, player.ID, player.Username, nullIfEmpty(reason))
	if err != nil {
		return fmt.Errorf("auth: record ban event: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit ban tx: %w", err)
	}
	return nil
}

// UnbanPlayer resets player status to active, clears ban info, and records an unbanned auth_event.
func (s *Service) UnbanPlayer(ctx context.Context, username string) error {
	username = strings.ToLower(username)

	player, err := s.GetPlayer(ctx, username)
	if err != nil {
		return err
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin unban tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		UPDATE players
		SET status = 'active', banned_until = NULL, ban_reason = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, player.ID)
	if err != nil {
		return fmt.Errorf("auth: update player unban: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO auth_events (player_id, username, event)
		VALUES (?, ?, 'unbanned')
	`, player.ID, player.Username)
	if err != nil {
		return fmt.Errorf("auth: record unban event: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit unban tx: %w", err)
	}
	return nil
}

// ResetPassword validates password length, hashes newPassword, updates player password_hash,
// and revokes all active sessions.
func (s *Service) ResetPassword(ctx context.Context, username, newPassword string) error {
	if len(newPassword) < 6 {
		return ErrPasswordTooShort
	}

	username = strings.ToLower(username)

	player, err := s.GetPlayer(ctx, username)
	if err != nil {
		return err
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin reset password tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		UPDATE players
		SET password_hash = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, hash, player.ID)
	if err != nil {
		return fmt.Errorf("auth: update password hash: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE player_id = ? AND revoked_at IS NULL
	`, player.ID)
	if err != nil {
		return fmt.Errorf("auth: revoke sessions on password reset: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit reset password tx: %w", err)
	}
	return nil
}

func (s *Service) getPlayerByID(ctx context.Context, id int64) (*Player, error) {
	row := s.db.SQL().QueryRowContext(ctx, `
		SELECT id, username, email, display_name, role, status,
		       banned_until, ban_reason, created_at, updated_at, last_login_at
		FROM players
		WHERE id = ?
	`, id)

	p, err := scanPlayerRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPlayerNotFound
		}
		return nil, fmt.Errorf("auth: get player by id: %w", err)
	}
	return p, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlayerRow(s rowScanner) (*Player, error) {
	var (
		p           Player
		email       sql.NullString
		displayName sql.NullString
		role        string
		status      string
		bannedUntil any
		banReason   sql.NullString
		createdAt   any
		updatedAt   any
		lastLoginAt any
	)

	err := s.Scan(
		&p.ID,
		&p.Username,
		&email,
		&displayName,
		&role,
		&status,
		&bannedUntil,
		&banReason,
		&createdAt,
		&updatedAt,
		&lastLoginAt,
	)
	if err != nil {
		return nil, err
	}

	if email.Valid {
		p.Email = &email.String
	}
	if displayName.Valid {
		p.DisplayName = displayName.String
	}
	p.Role = Role(role)
	p.Status = Status(status)
	if bannedUntil != nil {
		if t, err := toTime(bannedUntil); err == nil {
			p.BannedUntil = &t
		}
	}
	if banReason.Valid {
		p.BanReason = &banReason.String
	}
	if createdAt != nil {
		p.CreatedAt, _ = toTime(createdAt)
	}
	if updatedAt != nil {
		p.UpdatedAt, _ = toTime(updatedAt)
	}
	if lastLoginAt != nil {
		if t, err := toTime(lastLoginAt); err == nil {
			p.LastLoginAt = &t
		}
	}

	return &p, nil
}

func toTime(v any) (time.Time, error) {
	if v == nil {
		return time.Time{}, errors.New("nil time value")
	}
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), nil
	case string:
		if t == "" {
			return time.Time{}, errors.New("empty time string")
		}
		return parseTimeString(t)
	case []byte:
		if len(t) == 0 {
			return time.Time{}, errors.New("empty time bytes")
		}
		return parseTimeString(string(t))
	default:
		return time.Time{}, fmt.Errorf("unexpected time type %T", v)
	}
}

func parseTimeString(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
