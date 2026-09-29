CREATE TABLE chat_messages (
    id         INTEGER PRIMARY KEY,
    room_id    TEXT    NOT NULL,
    player_id  INTEGER NULL REFERENCES players(id) ON DELETE SET NULL,
    body       TEXT    NOT NULL CHECK (length(body) <= 2000),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_chat_messages_room_created ON chat_messages(room_id, created_at);
