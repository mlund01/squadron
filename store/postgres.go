package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const pgSchema = `
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
`

// NewPostgresBundle creates a Bundle backed by PostgreSQL
func NewPostgresBundle(connStr string) (*Bundle, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if _, err := db.Exec(pgSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// Migrate existing dev databases
	db.Exec(`ALTER TABLE mission_task_subtasks ADD COLUMN IF NOT EXISTS iteration_index INTEGER`)
	db.Exec(`CREATE TABLE IF NOT EXISTS task_inputs (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES mission_tasks(id), iteration_index INTEGER, objective TEXT NOT NULL, created_at TEXT)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_inputs_task ON task_inputs(task_id)`)

	return &Bundle{
		Missions: &PgMissionStore{db: db},
		Datasets: &PgDatasetStore{db: db},
		Sessions: &PgSessionStore{db: db},
		Events:   &PgEventStore{db: db},
		Costs:    &PgCostStore{db: db},
		closer:   db.Close,
	}, nil
}

// =============================================================================
// PgMissionStore
// =============================================================================

type PgMissionStore struct {
	db *sql.DB
}

func (s *PgMissionStore) CreateMission(name string, inputsJSON, configJSON string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO missions (id, mission_name, input_values_json, config_json, started_at) VALUES ($1, $2, $3, $4, $5)`,
		id, name, inputsJSON, configJSON, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create mission: %w", err)
	}
	return id, nil
}

func (s *PgMissionStore) UpdateMissionStatus(id, status string) error {
	var finishedAt *string
	if status == "completed" || status == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	_, err := s.db.Exec(
		`UPDATE missions SET status = $1, finished_at = $2 WHERE id = $3`,
		status, finishedAt, id,
	)
	return err
}

func (s *PgMissionStore) CreateTask(missionID, taskName, configJSON string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO mission_tasks (id, mission_id, task_name, config_json, started_at) VALUES ($1, $2, $3, $4, $5)`,
		id, missionID, taskName, configJSON, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return id, nil
}

func (s *PgMissionStore) UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error {
	var finishedAt *string
	if status == "completed" || status == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	_, err := s.db.Exec(
		`UPDATE mission_tasks SET status = $1, output_json = $2, error = $3, finished_at = $4 WHERE id = $5`,
		status, outputJSON, errMsg, finishedAt, id,
	)
	return err
}

func (s *PgMissionStore) UpdateTaskSummary(id, summary string) error {
	_, err := s.db.Exec(`UPDATE mission_tasks SET summary = $1 WHERE id = $2`, summary, id)
	return err
}

func (s *PgMissionStore) UpdateTaskStatusCAS(id, expectedOldStatus, newStatus string, outputJSON, errMsg *string) (bool, error) {
	var finishedAt *string
	if newStatus == "completed" || newStatus == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	result, err := s.db.Exec(
		`UPDATE mission_tasks SET status = $1, output_json = $2, error = $3, finished_at = $4 WHERE id = $5 AND status = $6`,
		newStatus, outputJSON, errMsg, finishedAt, id, expectedOldStatus,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *PgMissionStore) UpdateMissionStatusCAS(id, expectedOldStatus, newStatus string) (bool, error) {
	var finishedAt *string
	if newStatus == "completed" || newStatus == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	result, err := s.db.Exec(
		`UPDATE missions SET status = $1, finished_at = $2 WHERE id = $3 AND status = $4`,
		newStatus, finishedAt, id, expectedOldStatus,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *PgMissionStore) GetTasksByMission(missionID string) ([]MissionTask, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, summary, error FROM mission_tasks WHERE mission_id = $1`,
		missionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []MissionTask
	for rows.Next() {
		var t MissionTask
		var configJSON sql.NullString
		var startedAtStr, finishedAtStr sql.NullString
		var outputJSON, summary, errMsg sql.NullString

		if err := rows.Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &summary, &errMsg); err != nil {
			return nil, err
		}

		if configJSON.Valid {
			t.ConfigJSON = configJSON.String
		}
		t.StartedAt, _ = tsParseNull(startedAtStr)
		t.FinishedAt, _ = tsParseNull(finishedAtStr)
		if outputJSON.Valid {
			t.OutputJSON = &outputJSON.String
		}
		if errMsg.Valid {
			t.Error = &errMsg.String
		}

		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *PgMissionStore) StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, itemID *string, outputJSON string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO task_outputs (id, task_id, dataset_name, dataset_index, item_id, output_json, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, taskID, datasetName, datasetIndex, itemID, outputJSON, tsNow(),
	)
	return err
}

func (s *PgMissionStore) GetTask(id string) (*MissionTask, error) {
	var t MissionTask
	var configJSON sql.NullString
	var startedAtStr, finishedAtStr sql.NullString
	var outputJSON, summary, errMsg sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, summary, error FROM mission_tasks WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &summary, &errMsg)
	if err != nil {
		return nil, fmt.Errorf("task %q not found: %w", id, err)
	}

	if configJSON.Valid {
		t.ConfigJSON = configJSON.String
	}
	t.StartedAt, _ = tsParseNull(startedAtStr)
	t.FinishedAt, _ = tsParseNull(finishedAtStr)
	if outputJSON.Valid {
		t.OutputJSON = &outputJSON.String
	}
	if summary.Valid {
		t.Summary = &summary.String
	}
	if errMsg.Valid {
		t.Error = &errMsg.String
	}

	return &t, nil
}

func (s *PgMissionStore) GetTaskByName(missionID, taskName string) (*MissionTask, error) {
	var t MissionTask
	var configJSON sql.NullString
	var startedAtStr, finishedAtStr sql.NullString
	var outputJSON, summary, errMsg sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, summary, error FROM mission_tasks WHERE mission_id = $1 AND task_name = $2`,
		missionID, taskName,
	).Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &summary, &errMsg)
	if err != nil {
		return nil, fmt.Errorf("task '%s' not found: %w", taskName, err)
	}

	if configJSON.Valid {
		t.ConfigJSON = configJSON.String
	}
	t.StartedAt, _ = tsParseNull(startedAtStr)
	t.FinishedAt, _ = tsParseNull(finishedAtStr)
	if outputJSON.Valid {
		t.OutputJSON = &outputJSON.String
	}
	if summary.Valid {
		t.Summary = &summary.String
	}
	if errMsg.Valid {
		t.Error = &errMsg.String
	}

	return &t, nil
}

func (s *PgMissionStore) GetMission(id string) (*MissionRecord, error) {
	var m MissionRecord
	var inputsJSON, configJSON sql.NullString
	var startedAtStr string
	var finishedAtStr sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_name, status, input_values_json, config_json, started_at, finished_at FROM missions WHERE id = $1`,
		id,
	).Scan(&m.ID, &m.MissionName, &m.Status, &inputsJSON, &configJSON, &startedAtStr, &finishedAtStr)
	if err != nil {
		return nil, fmt.Errorf("mission not found: %w", err)
	}

	m.StartedAt, _ = tsParse(startedAtStr)
	if inputsJSON.Valid {
		m.InputValuesJSON = inputsJSON.String
	}
	if configJSON.Valid {
		m.ConfigJSON = configJSON.String
	}
	m.FinishedAt, _ = tsParseNull(finishedAtStr)

	return &m, nil
}

func (s *PgMissionStore) GetTaskOutputs(taskID string) ([]TaskOutputRow, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, dataset_name, dataset_index, item_id, output_json, created_at FROM task_outputs WHERE task_id = $1 ORDER BY dataset_index ASC, created_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var outputs []TaskOutputRow
	for rows.Next() {
		var o TaskOutputRow
		var datasetName, itemID, outputJSON sql.NullString
		var datasetIndex sql.NullInt64
		var createdAtStr string

		if err := rows.Scan(&o.ID, &o.TaskID, &datasetName, &datasetIndex, &itemID, &outputJSON, &createdAtStr); err != nil {
			return nil, err
		}
		o.CreatedAt, _ = tsParse(createdAtStr)

		if datasetName.Valid {
			o.DatasetName = &datasetName.String
		}
		if datasetIndex.Valid {
			idx := int(datasetIndex.Int64)
			o.DatasetIndex = &idx
		}
		if itemID.Valid {
			o.ItemID = &itemID.String
		}
		if outputJSON.Valid {
			o.OutputJSON = outputJSON.String
		}

		outputs = append(outputs, o)
	}
	return outputs, nil
}

// =============================================================================
// PgMissionStore.ListMissions
// =============================================================================

func (s *PgMissionStore) ListMissions(limit, offset int) ([]MissionRecord, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM missions`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count missions: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, mission_name, status, input_values_json, config_json, started_at, finished_at FROM missions ORDER BY started_at DESC, id DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var missions []MissionRecord
	for rows.Next() {
		var m MissionRecord
		var inputsJSON, configJSON sql.NullString
		var startedAtStr string
		var finishedAtStr sql.NullString

		if err := rows.Scan(&m.ID, &m.MissionName, &m.Status, &inputsJSON, &configJSON, &startedAtStr, &finishedAtStr); err != nil {
			return nil, 0, err
		}
		m.StartedAt, _ = tsParse(startedAtStr)
		if inputsJSON.Valid {
			m.InputValuesJSON = inputsJSON.String
		}
		if configJSON.Valid {
			m.ConfigJSON = configJSON.String
		}
		m.FinishedAt, _ = tsParseNull(finishedAtStr)
		missions = append(missions, m)
	}
	return missions, total, nil
}

func (s *PgMissionStore) StoreTaskInput(taskID string, iterationIndex *int, objective string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO task_inputs (id, task_id, iteration_index, objective, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, taskID, iterationIndex, objective, tsNow(),
	)
	return err
}

func (s *PgMissionStore) GetTaskInputs(taskID string) ([]TaskInput, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, iteration_index, objective, created_at FROM task_inputs WHERE task_id = $1 ORDER BY iteration_index ASC, created_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var inputs []TaskInput
	for rows.Next() {
		var ti TaskInput
		var iterIdx sql.NullInt64
		var createdAtStr string
		if err := rows.Scan(&ti.ID, &ti.TaskID, &iterIdx, &ti.Objective, &createdAtStr); err != nil {
			return nil, err
		}
		ti.CreatedAt, _ = tsParse(createdAtStr)
		if iterIdx.Valid {
			idx := int(iterIdx.Int64)
			ti.IterationIndex = &idx
		}
		inputs = append(inputs, ti)
	}
	return inputs, nil
}

func (s *PgMissionStore) SetSubtasks(taskID, sessionID string, iterationIndex *int, titles []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete existing subtasks for this task+session+iteration
	if iterationIndex != nil {
		tx.Exec(`DELETE FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index = $3`, taskID, sessionID, *iterationIndex)
	} else {
		tx.Exec(`DELETE FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index IS NULL`, taskID, sessionID)
	}

	now := tsNow()
	for i, title := range titles {
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		id := generateID()
		if _, err := tx.Exec(
			`INSERT INTO mission_task_subtasks (id, task_id, session_id, iteration_index, idx, title, status, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, taskID, sessionID, iterationIndex, i, title, status, now,
		); err != nil {
			return fmt.Errorf("insert subtask %d: %w", i, err)
		}
	}

	return tx.Commit()
}

func (s *PgMissionStore) GetSubtasks(taskID, sessionID string, iterationIndex *int) ([]Subtask, error) {
	var rows *sql.Rows
	var err error
	if iterationIndex != nil {
		rows, err = s.db.Query(
			`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index = $3 ORDER BY idx`,
			taskID, sessionID, *iterationIndex,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index IS NULL ORDER BY idx`,
			taskID, sessionID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubtasks(rows)
}

func (s *PgMissionStore) GetSubtasksByTask(taskID string) ([]Subtask, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = $1 ORDER BY idx`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubtasks(rows)
}

func (s *PgMissionStore) CompleteSubtask(taskID, sessionID string, iterationIndex *int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Find the first non-completed subtask
	var id string
	if iterationIndex != nil {
		err = tx.QueryRow(
			`SELECT id FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index = $3 AND status IN ('pending', 'in_progress') ORDER BY idx LIMIT 1`,
			taskID, sessionID, *iterationIndex,
		).Scan(&id)
	} else {
		err = tx.QueryRow(
			`SELECT id FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index IS NULL AND status IN ('pending', 'in_progress') ORDER BY idx LIMIT 1`,
			taskID, sessionID,
		).Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("no pending subtask to complete: %w", err)
	}

	// Mark it completed
	now := tsNow()
	if _, err := tx.Exec(`UPDATE mission_task_subtasks SET status = 'completed', completed_at = $1 WHERE id = $2`, now, id); err != nil {
		return fmt.Errorf("complete subtask: %w", err)
	}

	// Advance next pending to in_progress
	if iterationIndex != nil {
		tx.Exec(
			`UPDATE mission_task_subtasks SET status = 'in_progress' WHERE id = (SELECT id FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index = $3 AND status = 'pending' ORDER BY idx LIMIT 1)`,
			taskID, sessionID, *iterationIndex,
		)
	} else {
		tx.Exec(
			`UPDATE mission_task_subtasks SET status = 'in_progress' WHERE id = (SELECT id FROM mission_task_subtasks WHERE task_id = $1 AND session_id = $2 AND iteration_index IS NULL AND status = 'pending' ORDER BY idx LIMIT 1)`,
			taskID, sessionID,
		)
	}

	return tx.Commit()
}

func (s *PgMissionStore) StoreRouteDecision(missionID, routerTask, targetTask, condition string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO route_decisions (id, mission_id, router_task, target_task, condition_text, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, missionID, routerTask, targetTask, condition, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *PgMissionStore) GetRouteDecisions(missionID string) ([]RouteDecision, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, router_task, target_task, condition_text, created_at FROM route_decisions WHERE mission_id = $1 ORDER BY created_at`,
		missionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var decisions []RouteDecision
	for rows.Next() {
		var d RouteDecision
		var createdAt string
		if err := rows.Scan(&d.ID, &d.MissionID, &d.RouterTask, &d.TargetTask, &d.ConditionText, &createdAt); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}

// =============================================================================
// PgSessionStore
// =============================================================================

type PgSessionStore struct {
	db *sql.DB
}

func (s *PgSessionStore) CreateSession(taskID, role, agentName, model string, iterationIndex *int) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, task_id, role, agent_name, model, iteration_index, started_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, taskID, role, agentName, model, iterationIndex, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return id, nil
}

func (s *PgSessionStore) CompleteSession(id string, err error) {
	status := "completed"
	if err != nil {
		status = "failed"
	}
	s.db.Exec(
		`UPDATE sessions SET status = $1, finished_at = $2 WHERE id = $3`,
		status, tsNow(), id,
	)
}

func (s *PgSessionStore) ReopenSession(id string) {
	s.db.Exec(`UPDATE sessions SET status = 'running', finished_at = NULL WHERE id = $1`, id)
}

func (s *PgSessionStore) AppendMessage(sessionID, role, content string, createdAt, completedAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO session_messages (session_id, role, content, created_at, completed_at) VALUES ($1, $2, $3, $4, $5)`,
		sessionID, role, content, tsFrom(createdAt), tsFrom(completedAt),
	)
	return err
}

func (s *PgSessionStore) GetMessages(sessionID string) ([]SessionMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, created_at, completed_at FROM session_messages WHERE session_id = $1 ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []SessionMessage
	for rows.Next() {
		var m SessionMessage
		var createdAtStr string
		var completedAtStr sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &createdAtStr, &completedAtStr); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = tsParse(createdAtStr)
		if completedAtStr.Valid {
			m.CompletedAt, _ = tsParse(completedAtStr.String)
		} else {
			m.CompletedAt = m.CreatedAt
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (s *PgSessionStore) GetSessionsByTask(taskID string) ([]SessionInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, role, agent_name, model, status, iteration_index, started_at, finished_at FROM sessions WHERE task_id = $1`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var taskIDNull, agentName sql.NullString
		var iterIdx sql.NullInt64
		var startedAtStr string
		var finishedAtStr sql.NullString
		if err := rows.Scan(&si.ID, &taskIDNull, &si.Role, &agentName, &si.Model, &si.Status, &iterIdx, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		si.StartedAt, _ = tsParse(startedAtStr)
		if taskIDNull.Valid {
			si.TaskID = taskIDNull.String
		}
		if agentName.Valid {
			si.AgentName = agentName.String
		}
		if iterIdx.Valid {
			idx := int(iterIdx.Int64)
			si.IterationIndex = &idx
		}
		si.FinishedAt, _ = tsParseNull(finishedAtStr)
		sessions = append(sessions, si)
	}
	return sessions, nil
}

func (s *PgSessionStore) StoreToolResult(taskID, sessionID, toolCallId, toolName, inputParams, rawData string, startedAt, finishedAt time.Time) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO tool_results (id, task_id, session_id, tool_call_id, tool_name, input_params, raw_data, status, started_at, finished_at) VALUES ($1, $2, $3, $4, $5, $6, $7, 'completed', $8, $9)`,
		id, taskID, sessionID, toolCallId, toolName, inputParams, rawData, tsFrom(startedAt), tsFrom(finishedAt),
	)
	return err
}

func (s *PgSessionStore) StartToolCall(taskID, sessionID, toolCallId, toolName, inputParams string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO tool_results (id, task_id, session_id, tool_call_id, tool_name, input_params, status, started_at) VALUES ($1, $2, $3, $4, $5, $6, 'started', $7)`,
		id, taskID, sessionID, toolCallId, toolName, inputParams, tsNow(),
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *PgSessionStore) CompleteToolCall(id, rawData string) error {
	_, err := s.db.Exec(
		`UPDATE tool_results SET status = 'completed', raw_data = $1, finished_at = $2 WHERE id = $3`,
		rawData, tsNow(), id,
	)
	return err
}

func (s *PgSessionStore) GetToolResultsByTask(taskID string) ([]ToolResult, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, COALESCE(tool_call_id, ''), tool_name, input_params, raw_data, COALESCE(status, 'completed'), started_at, COALESCE(finished_at, started_at) FROM tool_results WHERE task_id = $1 ORDER BY started_at`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ToolResult
	for rows.Next() {
		var tr ToolResult
		var inputParams, rawData sql.NullString
		var startedAtStr, finishedAtStr string
		if err := rows.Scan(&tr.ID, &tr.TaskID, &tr.SessionID, &tr.ToolCallId, &tr.ToolName, &inputParams, &rawData, &tr.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		tr.InputParams = inputParams.String
		tr.RawData = rawData.String
		tr.StartedAt, _ = time.Parse(tsFormat, startedAtStr)
		tr.FinishedAt, _ = time.Parse(tsFormat, finishedAtStr)
		results = append(results, tr)
	}
	return results, nil
}

func (s *PgSessionStore) CreateChatSession(agentName, model string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, role, agent_name, model, started_at) VALUES ($1, 'chat', $2, $3, $4)`,
		id, agentName, model, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create chat session: %w", err)
	}
	return id, nil
}

func (s *PgSessionStore) ListChatSessions(agentName string, limit, offset int) ([]SessionInfo, int, error) {
	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM sessions WHERE role = 'chat' AND status != 'completed'`
	args := []any{}
	argIdx := 1
	if agentName != "" {
		countQuery += fmt.Sprintf(` AND agent_name = $%d`, argIdx)
		args = append(args, agentName)
		argIdx++
	}
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count chat sessions: %w", err)
	}

	// Fetch page
	query := `SELECT id, role, agent_name, model, status, started_at, finished_at FROM sessions WHERE role = 'chat' AND status != 'completed'`
	fetchArgs := []any{}
	fetchIdx := 1
	if agentName != "" {
		query += fmt.Sprintf(` AND agent_name = $%d`, fetchIdx)
		fetchArgs = append(fetchArgs, agentName)
		fetchIdx++
	}
	query += fmt.Sprintf(` ORDER BY started_at DESC LIMIT $%d OFFSET $%d`, fetchIdx, fetchIdx+1)
	fetchArgs = append(fetchArgs, limit, offset)

	rows, err := s.db.Query(query, fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var agName sql.NullString
		var startedAtStr sql.NullString
		var finishedAtStr sql.NullString
		if err := rows.Scan(&si.ID, &si.Role, &agName, &si.Model, &si.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, 0, err
		}
		if t, _ := tsParseNull(startedAtStr); t != nil {
			si.StartedAt = *t
		}
		if agName.Valid {
			si.AgentName = agName.String
		}
		si.FinishedAt, _ = tsParseNull(finishedAtStr)
		sessions = append(sessions, si)
	}
	return sessions, total, nil
}

// =============================================================================
// PgDatasetStore
// =============================================================================

type PgDatasetStore struct {
	db *sql.DB
}

func (s *PgDatasetStore) CreateDataset(missionID, name, description string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO datasets (id, mission_id, name, description, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, missionID, name, description, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create dataset: %w", err)
	}
	return id, nil
}

func (s *PgDatasetStore) AddItems(datasetID string, items []cty.Value) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get current max index
	var maxIndex int
	row := tx.QueryRow(`SELECT COALESCE(MAX(item_index), -1) FROM dataset_items WHERE dataset_id = $1`, datasetID)
	row.Scan(&maxIndex)

	stmt, err := tx.Prepare(`INSERT INTO dataset_items (dataset_id, item_index, item_json) VALUES ($1, $2, $3)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, item := range items {
		itemJSON, err := json.Marshal(ctyValueToGo(item))
		if err != nil {
			return fmt.Errorf("marshal item %d: %w", i, err)
		}
		if _, err := stmt.Exec(datasetID, maxIndex+1+i, string(itemJSON)); err != nil {
			return fmt.Errorf("insert item %d: %w", i, err)
		}
	}

	// Update item count
	tx.Exec(`UPDATE datasets SET item_count = (SELECT COUNT(*) FROM dataset_items WHERE dataset_id = $1) WHERE id = $2`,
		datasetID, datasetID)

	return tx.Commit()
}

func (s *PgDatasetStore) SetItems(datasetID string, items []cty.Value) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing items
	if _, err := tx.Exec(`DELETE FROM dataset_items WHERE dataset_id = $1`, datasetID); err != nil {
		return fmt.Errorf("clear items: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO dataset_items (dataset_id, item_index, item_json) VALUES ($1, $2, $3)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, item := range items {
		itemJSON, err := json.Marshal(ctyValueToGo(item))
		if err != nil {
			return fmt.Errorf("marshal item %d: %w", i, err)
		}
		if _, err := stmt.Exec(datasetID, i, string(itemJSON)); err != nil {
			return fmt.Errorf("insert item %d: %w", i, err)
		}
	}

	// Update item count
	tx.Exec(`UPDATE datasets SET item_count = $1 WHERE id = $2`, len(items), datasetID)

	return tx.Commit()
}

func (s *PgDatasetStore) GetItems(datasetID string, offset, limit int) ([]cty.Value, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = $1 ORDER BY item_index LIMIT $2 OFFSET $3`,
		datasetID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []cty.Value
	for rows.Next() {
		var itemJSON string
		if err := rows.Scan(&itemJSON); err != nil {
			return nil, err
		}
		items = append(items, goJSONToCty(itemJSON))
	}
	return items, nil
}

func (s *PgDatasetStore) GetItemCount(datasetID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dataset_items WHERE dataset_id = $1`, datasetID).Scan(&count)
	return count, err
}

func (s *PgDatasetStore) GetSample(datasetID string, count int) ([]cty.Value, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = $1 ORDER BY RANDOM() LIMIT $2`,
		datasetID, count,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []cty.Value
	for rows.Next() {
		var itemJSON string
		if err := rows.Scan(&itemJSON); err != nil {
			return nil, err
		}
		items = append(items, goJSONToCty(itemJSON))
	}
	return items, nil
}

func (s *PgDatasetStore) GetDatasetByName(missionID, name string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM datasets WHERE mission_id = $1 AND name = $2`,
		missionID, name,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("dataset '%s' not found: %w", name, err)
	}
	return id, nil
}

func (s *PgDatasetStore) ListDatasets(missionID string) ([]DatasetInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, item_count FROM datasets WHERE mission_id = $1`,
		missionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var infos []DatasetInfo
	for rows.Next() {
		var info DatasetInfo
		var desc sql.NullString
		if err := rows.Scan(&info.ID, &info.Name, &desc, &info.ItemCount); err != nil {
			return nil, err
		}
		if desc.Valid {
			info.Description = desc.String
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (s *PgDatasetStore) LockDataset(datasetID string) error {
	_, err := s.db.Exec(`UPDATE datasets SET locked = 1 WHERE id = $1`, datasetID)
	return err
}

func (s *PgDatasetStore) IsDatasetLocked(datasetID string) (bool, error) {
	var locked int
	err := s.db.QueryRow(`SELECT locked FROM datasets WHERE id = $1`, datasetID).Scan(&locked)
	if err != nil {
		return false, err
	}
	return locked == 1, nil
}

func (s *PgDatasetStore) GetItemsRaw(datasetID string, offset, limit int) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = $1 ORDER BY item_index LIMIT $2 OFFSET $3`,
		datasetID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var itemJSON string
		if err := rows.Scan(&itemJSON); err != nil {
			return nil, err
		}
		items = append(items, itemJSON)
	}
	return items, nil
}

// =============================================================================
// PgEventStore
// =============================================================================

type PgEventStore struct {
	db *sql.DB
}

func (s *PgEventStore) StoreEvent(event MissionEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO mission_events (id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID, event.MissionID, event.TaskID, event.SessionID, event.IterationIndex, event.EventType, event.DataJSON, tsFrom(event.CreatedAt),
	)
	return err
}

func (s *PgEventStore) GetEventsByMission(missionID string, limit, offset int) ([]MissionEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at FROM mission_events WHERE mission_id = $1 ORDER BY created_at ASC LIMIT $2 OFFSET $3`,
		missionID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *PgEventStore) GetEventsByTask(taskID string, limit, offset int) ([]MissionEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at FROM mission_events WHERE task_id = $1 ORDER BY created_at ASC LIMIT $2 OFFSET $3`,
		taskID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}
