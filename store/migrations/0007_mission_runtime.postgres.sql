CREATE TABLE IF NOT EXISTS runtime_checkpoints (
    run_id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL,
    payload_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_events (
    run_id TEXT NOT NULL REFERENCES runtime_checkpoints(run_id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL,
    PRIMARY KEY (run_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_runtime_events_run_revision
    ON runtime_events(run_id, revision);

ALTER TABLE turn_costs ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE turn_costs ALTER COLUMN session_id DROP NOT NULL;
ALTER TABLE turn_costs DROP CONSTRAINT IF EXISTS turn_costs_task_id_fkey;
ALTER TABLE turn_costs DROP CONSTRAINT IF EXISTS turn_costs_session_id_fkey;

ALTER TABLE human_input_requests ADD COLUMN deadline_at TIMESTAMPTZ;
ALTER TABLE human_input_requests ADD COLUMN expired_at TIMESTAMPTZ;
ALTER TABLE human_input_requests ADD COLUMN canceled_at TIMESTAMPTZ;
ALTER TABLE human_input_requests ADD COLUMN cancel_reason TEXT;
ALTER TABLE human_input_requests ADD COLUMN task_name TEXT;

CREATE INDEX IF NOT EXISTS idx_human_input_deadline
    ON human_input_requests(state, deadline_at);

CREATE TABLE mission_memory (
    mission_name TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    value_json TEXT NOT NULL,
    version BIGINT NOT NULL,
    expires_at TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (mission_name, memory_key)
);

CREATE TABLE mission_memory_writes (
    run_id TEXT NOT NULL,
    revision BIGINT NOT NULL,
    mission_name TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    version BIGINT NOT NULL,
    occurred_at TEXT NOT NULL,
    PRIMARY KEY (run_id, revision, mission_name, memory_key)
);

-- missions.thread_id is added in the same transaction by RunMigrations.
-- Main's version 5 may already have added it.
