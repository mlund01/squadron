CREATE TABLE IF NOT EXISTS human_input_requests (
    id TEXT PRIMARY KEY,
    mission_id TEXT,
    task_id TEXT,
    tool_call_id TEXT NOT NULL UNIQUE,
    question TEXT NOT NULL,
    short_summary TEXT,
    additional_context TEXT,
    choices_json TEXT,
    multi_select INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    resolved_at TEXT,
    response TEXT,
    responder_user_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_human_input_open
    ON human_input_requests(state, requested_at);

CREATE INDEX IF NOT EXISTS idx_human_input_mission
    ON human_input_requests(mission_id, state);
