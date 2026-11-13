CREATE TABLE IF NOT EXISTS runtime_checkpoints (
    run_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_events (
    run_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    revision INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL,
    PRIMARY KEY (run_id, sequence),
    FOREIGN KEY (run_id) REFERENCES runtime_checkpoints(run_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_runtime_events_run_revision
    ON runtime_events(run_id, revision);

CREATE TABLE turn_costs_runtime (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    task_id TEXT,
    session_id TEXT,
    mission_name TEXT NOT NULL,
    task_name TEXT NOT NULL,
    entity TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    input_cost REAL NOT NULL DEFAULT 0,
    output_cost REAL NOT NULL DEFAULT 0,
    cache_read_cost REAL NOT NULL DEFAULT 0,
    cache_write_cost REAL NOT NULL DEFAULT 0,
    total_cost REAL NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

INSERT INTO turn_costs_runtime SELECT * FROM turn_costs;
DROP TABLE turn_costs;
ALTER TABLE turn_costs_runtime RENAME TO turn_costs;

CREATE INDEX idx_turn_costs_mission ON turn_costs(mission_id);
CREATE INDEX idx_turn_costs_created ON turn_costs(created_at);
CREATE INDEX idx_turn_costs_model ON turn_costs(model);

ALTER TABLE human_input_requests ADD COLUMN deadline_at TEXT;
ALTER TABLE human_input_requests ADD COLUMN expired_at TEXT;
ALTER TABLE human_input_requests ADD COLUMN canceled_at TEXT;
ALTER TABLE human_input_requests ADD COLUMN cancel_reason TEXT;
ALTER TABLE human_input_requests ADD COLUMN task_name TEXT;

CREATE INDEX IF NOT EXISTS idx_human_input_deadline
    ON human_input_requests(state, deadline_at);

CREATE TABLE mission_memory (
    mission_name TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    value_json TEXT NOT NULL,
    version INTEGER NOT NULL,
    expires_at TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (mission_name, memory_key)
);

CREATE TABLE mission_memory_writes (
    run_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    mission_name TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    version INTEGER NOT NULL,
    occurred_at TEXT NOT NULL,
    PRIMARY KEY (run_id, revision, mission_name, memory_key)
);

-- missions.thread_id is added in the same transaction by RunMigrations.
-- Main's version 5 may already have added it, so SQLite needs a column check.
