package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
	_ "modernc.org/sqlite"
)

const schema = `
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
    id INTEGER PRIMARY KEY AUTOINCREMENT,
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

// NewSQLiteBundle creates a Bundle backed by SQLite at the given path
func NewSQLiteBundle(dbPath string) (*Bundle, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite only supports one writer at a time; limit the pool to a single
	// connection so concurrent goroutines serialize through database/sql
	// instead of getting SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// Migrate existing dev databases
	db.Exec(`ALTER TABLE mission_task_subtasks ADD COLUMN iteration_index INTEGER`)
	db.Exec(`CREATE TABLE IF NOT EXISTS task_inputs (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES mission_tasks(id), iteration_index INTEGER, objective TEXT NOT NULL, created_at TEXT)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_inputs_task ON task_inputs(task_id)`)

	return &Bundle{
		Missions: &SQLiteMissionStore{db: db},
		Datasets: &SQLiteDatasetStore{db: db},
		Sessions: &SQLiteSessionStore{db: db},
		Events:   &SQLiteEventStore{db: db},
		closer:   db.Close,
	}, nil
}

// =============================================================================
// SQLiteMissionStore
// =============================================================================

type SQLiteMissionStore struct {
	db *sql.DB
}

func (s *SQLiteMissionStore) CreateMission(name string, inputsJSON, configJSON string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO missions (id, mission_name, input_values_json, config_json, started_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, inputsJSON, configJSON, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create mission: %w", err)
	}
	return id, nil
}

func (s *SQLiteMissionStore) UpdateMissionStatus(id, status string) error {
	var finishedAt *string
	if status == "completed" || status == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	_, err := s.db.Exec(
		`UPDATE missions SET status = ?, finished_at = ? WHERE id = ?`,
		status, finishedAt, id,
	)
	return err
}

func (s *SQLiteMissionStore) CreateTask(missionID, taskName, configJSON string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO mission_tasks (id, mission_id, task_name, config_json, started_at) VALUES (?, ?, ?, ?, ?)`,
		id, missionID, taskName, configJSON, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return id, nil
}

func (s *SQLiteMissionStore) UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error {
	var finishedAt *string
	if status == "completed" || status == "failed" {
		s := tsNow()
		finishedAt = &s
	}
	_, err := s.db.Exec(
		`UPDATE mission_tasks SET status = ?, output_json = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, outputJSON, errMsg, finishedAt, id,
	)
	return err
}

func (s *SQLiteMissionStore) GetTasksByMission(missionID string) ([]MissionTask, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, error FROM mission_tasks WHERE mission_id = ?`,
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
		var outputJSON, errMsg sql.NullString

		if err := rows.Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &errMsg); err != nil {
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

func (s *SQLiteMissionStore) StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, itemID *string, outputJSON string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO task_outputs (id, task_id, dataset_name, dataset_index, item_id, output_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, datasetName, datasetIndex, itemID, outputJSON, tsNow(),
	)
	return err
}

func (s *SQLiteMissionStore) GetTask(id string) (*MissionTask, error) {
	var t MissionTask
	var configJSON sql.NullString
	var startedAtStr, finishedAtStr sql.NullString
	var outputJSON, errMsg sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, error FROM mission_tasks WHERE id = ?`,
		id,
	).Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &errMsg)
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
	if errMsg.Valid {
		t.Error = &errMsg.String
	}

	return &t, nil
}

func (s *SQLiteMissionStore) GetTaskByName(missionID, taskName string) (*MissionTask, error) {
	var t MissionTask
	var configJSON sql.NullString
	var startedAtStr, finishedAtStr sql.NullString
	var outputJSON, errMsg sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_id, task_name, status, config_json, started_at, finished_at, output_json, error FROM mission_tasks WHERE mission_id = ? AND task_name = ?`,
		missionID, taskName,
	).Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAtStr, &finishedAtStr, &outputJSON, &errMsg)
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
	if errMsg.Valid {
		t.Error = &errMsg.String
	}

	return &t, nil
}

func (s *SQLiteMissionStore) GetMission(id string) (*MissionRecord, error) {
	var m MissionRecord
	var inputsJSON, configJSON sql.NullString
	var startedAtStr string
	var finishedAtStr sql.NullString

	err := s.db.QueryRow(
		`SELECT id, mission_name, status, input_values_json, config_json, started_at, finished_at FROM missions WHERE id = ?`,
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

func (s *SQLiteMissionStore) GetTaskOutputs(taskID string) ([]TaskOutputRow, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, dataset_name, dataset_index, item_id, output_json, created_at FROM task_outputs WHERE task_id = ? ORDER BY dataset_index ASC, created_at ASC`,
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
// SQLiteSessionStore
// =============================================================================

type SQLiteSessionStore struct {
	db *sql.DB
}

func (s *SQLiteSessionStore) CreateSession(taskID, role, agentName, model string, iterationIndex *int) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, task_id, role, agent_name, model, iteration_index, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, role, agentName, model, iterationIndex, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return id, nil
}

func (s *SQLiteSessionStore) CompleteSession(id string, err error) {
	status := "completed"
	if err != nil {
		status = "failed"
	}
	s.db.Exec(
		`UPDATE sessions SET status = ?, finished_at = ? WHERE id = ?`,
		status, tsNow(), id,
	)
}

func (s *SQLiteSessionStore) ReopenSession(id string) {
	s.db.Exec(`UPDATE sessions SET status = 'running', finished_at = NULL WHERE id = ?`, id)
}

func (s *SQLiteSessionStore) AppendMessage(sessionID, role, content string, createdAt, completedAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO session_messages (session_id, role, content, created_at, completed_at) VALUES (?, ?, ?, ?, ?)`,
		sessionID, role, content, tsFrom(createdAt), tsFrom(completedAt),
	)
	return err
}

func (s *SQLiteSessionStore) GetMessages(sessionID string) ([]SessionMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, created_at, completed_at FROM session_messages WHERE session_id = ? ORDER BY id`,
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

func (s *SQLiteSessionStore) GetSessionsByTask(taskID string) ([]SessionInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, role, agent_name, model, status, iteration_index, started_at, finished_at FROM sessions WHERE task_id = ?`,
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

func (s *SQLiteSessionStore) StoreToolResult(taskID, sessionID, toolName, inputParams, rawData string, startedAt, finishedAt time.Time) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO tool_results (id, task_id, session_id, tool_name, input_params, raw_data, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, sessionID, toolName, inputParams, rawData, tsFrom(startedAt), tsFrom(finishedAt),
	)
	return err
}

func (s *SQLiteSessionStore) GetToolResultsByTask(taskID string) ([]ToolResult, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, tool_name, input_params, raw_data, started_at, finished_at FROM tool_results WHERE task_id = ? ORDER BY started_at`,
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
		if err := rows.Scan(&tr.ID, &tr.TaskID, &tr.SessionID, &tr.ToolName, &inputParams, &rawData, &startedAtStr, &finishedAtStr); err != nil {
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

func (s *SQLiteSessionStore) CreateChatSession(agentName, model string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, role, agent_name, model, started_at) VALUES (?, 'chat', ?, ?, ?)`,
		id, agentName, model, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create chat session: %w", err)
	}
	return id, nil
}

func (s *SQLiteSessionStore) ListChatSessions(agentName string, limit, offset int) ([]SessionInfo, int, error) {
	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM sessions WHERE role = 'chat' AND status != 'completed'`
	args := []any{}
	if agentName != "" {
		countQuery += ` AND agent_name = ?`
		args = append(args, agentName)
	}
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count chat sessions: %w", err)
	}

	// Fetch page
	query := `SELECT id, role, agent_name, model, status, started_at, finished_at FROM sessions WHERE role = 'chat' AND status != 'completed'`
	fetchArgs := []any{}
	if agentName != "" {
		query += ` AND agent_name = ?`
		fetchArgs = append(fetchArgs, agentName)
	}
	query += ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
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
// SQLiteDatasetStore
// =============================================================================

type SQLiteDatasetStore struct {
	db *sql.DB
}

func (s *SQLiteDatasetStore) CreateDataset(missionID, name, description string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO datasets (id, mission_id, name, description, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, missionID, name, description, tsNow(),
	)
	if err != nil {
		return "", fmt.Errorf("create dataset: %w", err)
	}
	return id, nil
}

func (s *SQLiteDatasetStore) AddItems(datasetID string, items []cty.Value) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get current max index
	var maxIndex int
	row := tx.QueryRow(`SELECT COALESCE(MAX(item_index), -1) FROM dataset_items WHERE dataset_id = ?`, datasetID)
	row.Scan(&maxIndex)

	stmt, err := tx.Prepare(`INSERT INTO dataset_items (dataset_id, item_index, item_json) VALUES (?, ?, ?)`)
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
	tx.Exec(`UPDATE datasets SET item_count = (SELECT COUNT(*) FROM dataset_items WHERE dataset_id = ?) WHERE id = ?`,
		datasetID, datasetID)

	return tx.Commit()
}

func (s *SQLiteDatasetStore) SetItems(datasetID string, items []cty.Value) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing items
	if _, err := tx.Exec(`DELETE FROM dataset_items WHERE dataset_id = ?`, datasetID); err != nil {
		return fmt.Errorf("clear items: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO dataset_items (dataset_id, item_index, item_json) VALUES (?, ?, ?)`)
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
	tx.Exec(`UPDATE datasets SET item_count = ? WHERE id = ?`, len(items), datasetID)

	return tx.Commit()
}

func (s *SQLiteDatasetStore) GetItems(datasetID string, offset, limit int) ([]cty.Value, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = ? ORDER BY item_index LIMIT ? OFFSET ?`,
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

func (s *SQLiteDatasetStore) GetItemCount(datasetID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dataset_items WHERE dataset_id = ?`, datasetID).Scan(&count)
	return count, err
}

func (s *SQLiteDatasetStore) GetSample(datasetID string, count int) ([]cty.Value, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = ? ORDER BY RANDOM() LIMIT ?`,
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

func (s *SQLiteDatasetStore) GetDatasetByName(missionID, name string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM datasets WHERE mission_id = ? AND name = ?`,
		missionID, name,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("dataset '%s' not found: %w", name, err)
	}
	return id, nil
}

func (s *SQLiteDatasetStore) ListDatasets(missionID string) ([]DatasetInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, item_count FROM datasets WHERE mission_id = ?`,
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

func (s *SQLiteDatasetStore) LockDataset(datasetID string) error {
	_, err := s.db.Exec(`UPDATE datasets SET locked = 1 WHERE id = ?`, datasetID)
	return err
}

func (s *SQLiteDatasetStore) IsDatasetLocked(datasetID string) (bool, error) {
	var locked int
	err := s.db.QueryRow(`SELECT locked FROM datasets WHERE id = ?`, datasetID).Scan(&locked)
	if err != nil {
		return false, err
	}
	return locked == 1, nil
}

func (s *SQLiteDatasetStore) GetItemsRaw(datasetID string, offset, limit int) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT item_json FROM dataset_items WHERE dataset_id = ? ORDER BY item_index LIMIT ? OFFSET ?`,
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
// SQLiteMissionStore.ListMissions
// =============================================================================

func (s *SQLiteMissionStore) ListMissions(limit, offset int) ([]MissionRecord, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM missions`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count missions: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, mission_name, status, input_values_json, config_json, started_at, finished_at FROM missions ORDER BY started_at DESC, rowid DESC LIMIT ? OFFSET ?`,
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

func (s *SQLiteMissionStore) StoreTaskInput(taskID string, iterationIndex *int, objective string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO task_inputs (id, task_id, iteration_index, objective, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, taskID, iterationIndex, objective, tsNow(),
	)
	return err
}

func (s *SQLiteMissionStore) GetTaskInputs(taskID string) ([]TaskInput, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, iteration_index, objective, created_at FROM task_inputs WHERE task_id = ? ORDER BY iteration_index ASC, created_at ASC`,
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

func (s *SQLiteMissionStore) SetSubtasks(taskID, sessionID string, iterationIndex *int, titles []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete existing subtasks for this task+session+iteration
	if iterationIndex != nil {
		tx.Exec(`DELETE FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index = ?`, taskID, sessionID, *iterationIndex)
	} else {
		tx.Exec(`DELETE FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index IS NULL`, taskID, sessionID)
	}

	now := tsNow()
	for i, title := range titles {
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		id := generateID()
		if _, err := tx.Exec(
			`INSERT INTO mission_task_subtasks (id, task_id, session_id, iteration_index, idx, title, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, taskID, sessionID, iterationIndex, i, title, status, now,
		); err != nil {
			return fmt.Errorf("insert subtask %d: %w", i, err)
		}
	}

	return tx.Commit()
}

func (s *SQLiteMissionStore) GetSubtasks(taskID, sessionID string, iterationIndex *int) ([]Subtask, error) {
	var rows *sql.Rows
	var err error
	if iterationIndex != nil {
		rows, err = s.db.Query(
			`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index = ? ORDER BY idx`,
			taskID, sessionID, *iterationIndex,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index IS NULL ORDER BY idx`,
			taskID, sessionID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubtasks(rows)
}

func (s *SQLiteMissionStore) GetSubtasksByTask(taskID string) ([]Subtask, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at FROM mission_task_subtasks WHERE task_id = ? ORDER BY idx`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubtasks(rows)
}

func (s *SQLiteMissionStore) CompleteSubtask(taskID, sessionID string, iterationIndex *int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Find the first non-completed subtask
	var id string
	if iterationIndex != nil {
		err = tx.QueryRow(
			`SELECT id FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index = ? AND status IN ('pending', 'in_progress') ORDER BY idx LIMIT 1`,
			taskID, sessionID, *iterationIndex,
		).Scan(&id)
	} else {
		err = tx.QueryRow(
			`SELECT id FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index IS NULL AND status IN ('pending', 'in_progress') ORDER BY idx LIMIT 1`,
			taskID, sessionID,
		).Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("no pending subtask to complete: %w", err)
	}

	// Mark it completed
	now := tsNow()
	if _, err := tx.Exec(`UPDATE mission_task_subtasks SET status = 'completed', completed_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("complete subtask: %w", err)
	}

	// Advance next pending to in_progress
	if iterationIndex != nil {
		tx.Exec(
			`UPDATE mission_task_subtasks SET status = 'in_progress' WHERE id = (SELECT id FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index = ? AND status = 'pending' ORDER BY idx LIMIT 1)`,
			taskID, sessionID, *iterationIndex,
		)
	} else {
		tx.Exec(
			`UPDATE mission_task_subtasks SET status = 'in_progress' WHERE id = (SELECT id FROM mission_task_subtasks WHERE task_id = ? AND session_id = ? AND iteration_index IS NULL AND status = 'pending' ORDER BY idx LIMIT 1)`,
			taskID, sessionID,
		)
	}

	return tx.Commit()
}

// =============================================================================
// SQLiteEventStore
// =============================================================================

type SQLiteEventStore struct {
	db *sql.DB
}

func (s *SQLiteEventStore) StoreEvent(event MissionEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO mission_events (id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.MissionID, event.TaskID, event.SessionID, event.IterationIndex, event.EventType, event.DataJSON, tsFrom(event.CreatedAt),
	)
	return err
}

func (s *SQLiteEventStore) GetEventsByMission(missionID string, limit, offset int) ([]MissionEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at FROM mission_events WHERE mission_id = ? ORDER BY created_at ASC LIMIT ? OFFSET ?`,
		missionID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *SQLiteEventStore) GetEventsByTask(taskID string, limit, offset int) ([]MissionEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_id, session_id, iteration_index, event_type, data_json, created_at FROM mission_events WHERE task_id = ? ORDER BY created_at ASC LIMIT ? OFFSET ?`,
		taskID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}


