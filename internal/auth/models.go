package auth

import "time"

// Role defines player access permissions.
type Role string

const (
	RolePlayer Role = "player"
	RoleAdmin  Role = "admin"
)

// Status defines player account states.
type Status string

const (
	StatusActive Status = "active"
	StatusBanned Status = "banned"
)

// Player represents an account record in the database.
type Player struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Email       *string    `json:"email,omitempty"`
	DisplayName string     `json:"display_name"`
	Role        Role       `json:"role"`
	Status      Status     `json:"status"`
	BannedUntil *time.Time `json:"banned_until,omitempty"`
	BanReason   *string    `json:"ban_reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// Session represents an active player login session.
type Session struct {
	ID        int64     `json:"id"`
	PlayerID  int64     `json:"player_id"`
	Token     string    `json:"token,omitempty"` // only populated on login/creation
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
