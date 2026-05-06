CREATE TABLE IF NOT EXISTS session_message_parts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES session_messages(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    type TEXT NOT NULL,

    text TEXT,

    tool_use_id TEXT,
    tool_name TEXT,
    tool_input_json TEXT,
    thought_signature BLOB,
    is_error INTEGER,

    image_data TEXT,
    image_media_type TEXT,

    thinking_signature TEXT,
    thinking_redacted_data TEXT,
    provider_id TEXT,
    encrypted_content TEXT,

    provider_name TEXT,
    provider_type TEXT,
    provider_data_json TEXT,

    UNIQUE (message_id, position)
);

CREATE INDEX IF NOT EXISTS idx_session_message_parts_message_id
    ON session_message_parts(message_id);
