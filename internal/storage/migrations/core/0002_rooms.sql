CREATE TABLE game_rooms (
    id          TEXT    PRIMARY KEY,
    name        TEXT,
    mode        TEXT    NOT NULL,
    max_players INTEGER NOT NULL,
    status      TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'running', 'closed')),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at   DATETIME NULL
);
