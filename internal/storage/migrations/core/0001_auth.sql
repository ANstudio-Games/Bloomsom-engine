-- Core auth tables (spec section 4). All timestamps are UTC.
CREATE TABLE players (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    email         TEXT    NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    display_name  TEXT,
    role          TEXT    NOT NULL DEFAULT 'player' CHECK (role IN ('player', 'admin')),
    status        TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'banned')),
    banned_until  DATETIME NULL,
    ban_reason    TEXT    NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME NULL
);

CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY,
    player_id      INTEGER NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    token_hash     TEXT    NOT NULL UNIQUE,
    client_version TEXT,
    remote_addr    TEXT,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at     DATETIME NOT NULL,
    last_seen_at   DATETIME,
    revoked_at     DATETIME NULL
);

CREATE INDEX idx_sessions_player_id ON sessions(player_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE auth_events (
    id          INTEGER PRIMARY KEY,
    player_id   INTEGER NULL REFERENCES players(id) ON DELETE SET NULL,
    username    TEXT,
    event       TEXT    NOT NULL CHECK (event IN ('register', 'login_ok', 'login_fail', 'logout', 'banned', 'unbanned')),
    reason      TEXT    NULL,
    remote_addr TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Support login rate limiting by username and by remote address.
CREATE INDEX idx_auth_events_username_created ON auth_events(username, created_at);
CREATE INDEX idx_auth_events_remote_created ON auth_events(remote_addr, created_at);
