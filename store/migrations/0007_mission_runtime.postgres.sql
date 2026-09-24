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
