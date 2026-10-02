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
