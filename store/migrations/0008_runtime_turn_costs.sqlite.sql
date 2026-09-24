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
