CREATE TABLE IF NOT EXISTS missions (
    id TEXT PRIMARY KEY,
    mission_name TEXT NOT NULL,
    status TEXT DEFAULT 'running',
    input_values_json TEXT,
    config_json TEXT,
    started_at TEXT,
    finished_at TEXT
);

CREATE TABLE IF NOT EXISTS mission_tasks (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    task_name TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    config_json TEXT,
    started_at TEXT,
    finished_at TEXT,
    summary TEXT,
    output_json TEXT,
    error TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES mission_tasks(id),
    role TEXT NOT NULL,
    agent_name TEXT,
    model TEXT,
    status TEXT DEFAULT 'running',
    iteration_index INTEGER,
    started_at TEXT,
    finished_at TEXT
);

CREATE TABLE IF NOT EXISTS session_messages (
    id SERIAL PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_session_messages_session ON session_messages(session_id);

CREATE TABLE IF NOT EXISTS turn_costs (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    session_id TEXT NOT NULL REFERENCES sessions(id),
    mission_name TEXT NOT NULL,
    task_name TEXT NOT NULL,
    entity TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    input_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    cache_read_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    cache_write_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_turn_costs_mission ON turn_costs(mission_id);
CREATE INDEX IF NOT EXISTS idx_turn_costs_created ON turn_costs(created_at);
CREATE INDEX IF NOT EXISTS idx_turn_costs_model ON turn_costs(model);

CREATE TABLE IF NOT EXISTS tool_results (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    session_id TEXT NOT NULL REFERENCES sessions(id),
    tool_name TEXT NOT NULL,
    input_params TEXT,
    raw_data TEXT,
    started_at TEXT,
    finished_at TEXT
);

CREATE TABLE IF NOT EXISTS task_outputs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    dataset_name TEXT,
    dataset_index INTEGER,
    item_id TEXT,
    output_json TEXT,
    summary TEXT,
    created_at TEXT
);

CREATE TABLE IF NOT EXISTS datasets (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    name TEXT NOT NULL,
    description TEXT,
    item_count INTEGER DEFAULT 0,
    locked INTEGER DEFAULT 0,
    created_at TEXT,
    UNIQUE(mission_id, name)
);

CREATE TABLE IF NOT EXISTS dataset_items (
    dataset_id TEXT NOT NULL REFERENCES datasets(id),
    item_index INTEGER NOT NULL,
    item_json TEXT NOT NULL,
    PRIMARY KEY (dataset_id, item_index)
);

CREATE TABLE IF NOT EXISTS mission_task_subtasks (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    session_id TEXT NOT NULL REFERENCES sessions(id),
    iteration_index INTEGER,
    idx INTEGER NOT NULL,
    title TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    created_at TEXT,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_subtasks_task_session ON mission_task_subtasks(task_id, session_id);

CREATE TABLE IF NOT EXISTS task_inputs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    iteration_index INTEGER,
    objective TEXT NOT NULL,
    created_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_task_inputs_task ON task_inputs(task_id);

CREATE TABLE IF NOT EXISTS mission_events (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    task_id TEXT REFERENCES mission_tasks(id),
    session_id TEXT REFERENCES sessions(id),
    iteration_index INTEGER,
    event_type TEXT NOT NULL,
    data_json TEXT NOT NULL,
    created_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_mission_events_mission ON mission_events(mission_id);
CREATE INDEX IF NOT EXISTS idx_mission_events_task ON mission_events(task_id);
CREATE INDEX IF NOT EXISTS idx_mission_events_type ON mission_events(mission_id, event_type);

CREATE TABLE IF NOT EXISTS route_decisions (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    router_task TEXT NOT NULL,
    target_task TEXT NOT NULL,
    condition_text TEXT NOT NULL,
    created_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_route_decisions_mission ON route_decisions(mission_id);
