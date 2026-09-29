CREATE TABLE matches (
    id                     INTEGER PRIMARY KEY,
    room_id                TEXT    NOT NULL,
    status                 TEXT    NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'active', 'finished', 'abandoned')),
    current_turn_player_id INTEGER NULL REFERENCES players(id) ON DELETE SET NULL,
    winner_player_id       INTEGER NULL REFERENCES players(id) ON DELETE SET NULL,
    state_json             TEXT    NOT NULL DEFAULT '{}',
    created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at            DATETIME NULL
);

CREATE INDEX idx_matches_room ON matches(room_id);

CREATE TABLE match_moves (
    id         INTEGER PRIMARY KEY,
    match_id   INTEGER NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    player_id  INTEGER NULL REFERENCES players(id) ON DELETE SET NULL,
    move_no    INTEGER NOT NULL,
    move_json  TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (match_id, move_no)
);
