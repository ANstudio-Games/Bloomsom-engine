CREATE TABLE player_states (
    player_id  INTEGER NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    room_id    TEXT    NOT NULL,
    data_json  TEXT    NOT NULL DEFAULT '{}',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (player_id, room_id)
);

CREATE TABLE match_results (
    id          INTEGER PRIMARY KEY,
    room_id     TEXT    NOT NULL,
    player_id   INTEGER NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    score       INTEGER NOT NULL DEFAULT 0,
    kills       INTEGER NOT NULL DEFAULT 0,
    deaths      INTEGER NOT NULL DEFAULT 0,
    placement   INTEGER,
    finished_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_match_results_room ON match_results(room_id);
CREATE INDEX idx_match_results_player ON match_results(player_id);
