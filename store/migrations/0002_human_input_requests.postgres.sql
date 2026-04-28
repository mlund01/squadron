CREATE TABLE IF NOT EXISTS human_input_requests (
    id TEXT PRIMARY KEY,
    mission_id TEXT,
    task_id TEXT,
    tool_call_id TEXT NOT NULL UNIQUE,
    question TEXT NOT NULL,
    choices_json TEXT,
    state TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    response TEXT,
    responder_user_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_human_input_open
    ON human_input_requests(state, requested_at);

CREATE INDEX IF NOT EXISTS idx_human_input_mission
    ON human_input_requests(mission_id, state);
