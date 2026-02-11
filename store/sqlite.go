package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS missions (
    id TEXT PRIMARY KEY,
    mission_name TEXT NOT NULL,
    status TEXT DEFAULT 'running',
    input_values_json TEXT,
    config_json TEXT,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE TABLE IF NOT EXISTS mission_tasks (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    task_name TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    config_json TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    output_json TEXT,
    error TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    role TEXT NOT NULL,
    agent_name TEXT,
    model TEXT,
    status TEXT DEFAULT 'running',
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE TABLE IF NOT EXISTS session_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_session_messages_session ON session_messages(session_id);

CREATE TABLE IF NOT EXISTS tool_results (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    tool_name TEXT,
    type TEXT,
    size INTEGER,
    raw_data TEXT
);

CREATE TABLE IF NOT EXISTS task_outputs (
    task_id TEXT PRIMARY KEY REFERENCES mission_tasks(id),
    is_iterated INTEGER,
    output_json TEXT,
    iterations_json TEXT
);

CREATE TABLE IF NOT EXISTS datasets (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL REFERENCES missions(id),
    name TEXT NOT NULL,
    description TEXT,
    item_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(mission_id, name)
);

CREATE TABLE IF NOT EXISTS dataset_items (
    dataset_id TEXT NOT NULL REFERENCES datasets(id),
    item_index INTEGER NOT NULL,
    item_json TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    output_json TEXT,
    error TEXT,
    PRIMARY KEY (dataset_id, item_index)
);

CREATE TABLE IF NOT EXISTS questions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES mission_tasks(id),
    iteration_key TEXT,
    question TEXT NOT NULL,
    answer TEXT,
    ready INTEGER DEFAULT 0
);
`

// NewSQLiteBundle creates a Bundle backed by SQLite at the given path
func NewSQLiteBundle(dbPath string) (*Bundle, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Bundle{
		Questions: &SQLiteQuestionStore{db: db},
		Missions:  &SQLiteMissionStore{db: db},
		Datasets:  &SQLiteDatasetStore{db: db},
		Sessions:  &SQLiteSessionStore{db: db},
		closer:    db.Close,
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
		`INSERT INTO missions (id, mission_name, input_values_json, config_json) VALUES (?, ?, ?, ?)`,
		id, name, inputsJSON, configJSON,
	)
	if err != nil {
		return "", fmt.Errorf("create mission: %w", err)
	}
	return id, nil
}

func (s *SQLiteMissionStore) UpdateMissionStatus(id, status string) error {
	var finishedAt *time.Time
	if status == "completed" || status == "failed" {
		now := time.Now()
		finishedAt = &now
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
		id, missionID, taskName, configJSON, time.Now(),
	)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return id, nil
}

func (s *SQLiteMissionStore) UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error {
	var finishedAt *time.Time
	if status == "completed" || status == "failed" {
		now := time.Now()
		finishedAt = &now
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
		var startedAt, finishedAt sql.NullTime
		var outputJSON, errMsg sql.NullString

		if err := rows.Scan(&t.ID, &t.MissionID, &t.TaskName, &t.Status, &configJSON, &startedAt, &finishedAt, &outputJSON, &errMsg); err != nil {
			return nil, err
		}

		if configJSON.Valid {
			t.ConfigJSON = configJSON.String
		}
		if startedAt.Valid {
			t.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			t.FinishedAt = &finishedAt.Time
		}
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

func (s *SQLiteMissionStore) StoreTaskOutput(taskID string, isIterated bool, outputJSON, iterationsJSON *string) error {
	isIteratedInt := 0
	if isIterated {
		isIteratedInt = 1
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO task_outputs (task_id, is_iterated, output_json, iterations_json) VALUES (?, ?, ?, ?)`,
		taskID, isIteratedInt, outputJSON, iterationsJSON,
	)
	return err
}

// =============================================================================
// SQLiteSessionStore
// =============================================================================

type SQLiteSessionStore struct {
	db *sql.DB
}

func (s *SQLiteSessionStore) CreateSession(taskID, role, agentName, model string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, task_id, role, agent_name, model) VALUES (?, ?, ?, ?, ?)`,
		id, taskID, role, agentName, model,
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
		status, time.Now(), id,
	)
}

func (s *SQLiteSessionStore) AppendMessage(sessionID, role, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO session_messages (session_id, role, content) VALUES (?, ?, ?)`,
		sessionID, role, content,
	)
	return err
}

func (s *SQLiteSessionStore) GetMessages(sessionID string) ([]SessionMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, created_at FROM session_messages WHERE session_id = ? ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []SessionMessage
	for rows.Next() {
		var m SessionMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (s *SQLiteSessionStore) GetSessionsByTask(taskID string) ([]SessionInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, role, agent_name, model, status, started_at FROM sessions WHERE task_id = ?`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var agentName sql.NullString
		if err := rows.Scan(&si.ID, &si.TaskID, &si.Role, &agentName, &si.Model, &si.Status, &si.StartedAt); err != nil {
			return nil, err
		}
		if agentName.Valid {
			si.AgentName = agentName.String
		}
		sessions = append(sessions, si)
	}
	return sessions, nil
}

func (s *SQLiteSessionStore) StoreToolResult(sessionID, toolName, resultType string, size int, rawData string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO tool_results (id, session_id, tool_name, type, size, raw_data) VALUES (?, ?, ?, ?, ?, ?)`,
		id, sessionID, toolName, resultType, size, rawData,
	)
	return err
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
		`INSERT INTO datasets (id, mission_id, name, description) VALUES (?, ?, ?, ?)`,
		id, missionID, name, description,
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
		`SELECT name, description, item_count FROM datasets WHERE mission_id = ?`,
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
		if err := rows.Scan(&info.Name, &desc, &info.ItemCount); err != nil {
			return nil, err
		}
		if desc.Valid {
			info.Description = desc.String
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (s *SQLiteDatasetStore) UpdateItemStatus(datasetID string, index int, status string, outputJSON, errMsg *string) error {
	_, err := s.db.Exec(
		`UPDATE dataset_items SET status = ?, output_json = ?, error = ? WHERE dataset_id = ? AND item_index = ?`,
		status, outputJSON, errMsg, datasetID, index,
	)
	return err
}

// =============================================================================
// SQLiteQuestionStore
// =============================================================================

type SQLiteQuestionStore struct {
	db *sql.DB
}

func (s *SQLiteQuestionStore) StoreQuestion(taskID, iterationKey, question string) (string, error) {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO questions (id, task_id, iteration_key, question) VALUES (?, ?, ?, ?)`,
		id, taskID, iterationKey, question,
	)
	if err != nil {
		return "", fmt.Errorf("store question: %w", err)
	}
	return id, nil
}

func (s *SQLiteQuestionStore) SetAnswer(id, answer string) error {
	_, err := s.db.Exec(
		`UPDATE questions SET answer = ?, ready = 1 WHERE id = ?`,
		answer, id,
	)
	return err
}

func (s *SQLiteQuestionStore) GetAnswer(id string) (string, bool, error) {
	var answer sql.NullString
	var ready int
	err := s.db.QueryRow(`SELECT answer, ready FROM questions WHERE id = ?`, id).Scan(&answer, &ready)
	if err != nil {
		return "", false, err
	}
	if answer.Valid {
		return answer.String, ready != 0, nil
	}
	return "", false, nil
}

func (s *SQLiteQuestionStore) ListQuestions(taskID, excludeIterationKey string) ([]QuestionInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, question, ready FROM questions WHERE task_id = ? AND iteration_key != ?`,
		taskID, excludeIterationKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var infos []QuestionInfo
	for rows.Next() {
		var info QuestionInfo
		var ready int
		if err := rows.Scan(&info.ID, &info.Question, &ready); err != nil {
			return nil, err
		}
		info.HasAnswer = ready != 0
		infos = append(infos, info)
	}
	return infos, nil
}

// =============================================================================
// cty conversion helpers
// =============================================================================

// ctyValueToGo converts a cty.Value to a Go value for JSON serialization
func ctyValueToGo(val cty.Value) any {
	if val.IsNull() || !val.IsKnown() {
		return nil
	}

	switch {
	case val.Type() == cty.String:
		return val.AsString()
	case val.Type() == cty.Number:
		f, _ := val.AsBigFloat().Float64()
		return f
	case val.Type() == cty.Bool:
		return val.True()
	case val.Type().IsObjectType() || val.Type().IsMapType():
		result := make(map[string]any)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			result[k.AsString()] = ctyValueToGo(v)
		}
		return result
	case val.Type().IsTupleType() || val.Type().IsListType():
		var result []any
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			result = append(result, ctyValueToGo(v))
		}
		return result
	default:
		return nil
	}
}

// goToCtyValue converts a Go value to cty.Value
func goToCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case string:
		return cty.StringVal(val)
	case float64:
		return cty.NumberFloatVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case bool:
		return cty.BoolVal(val)
	case map[string]any:
		if len(val) == 0 {
			return cty.EmptyObjectVal
		}
		vals := make(map[string]cty.Value)
		for k, v := range val {
			vals[k] = goToCtyValue(v)
		}
		return cty.ObjectVal(vals)
	case []any:
		if len(val) == 0 {
			return cty.ListValEmpty(cty.DynamicPseudoType)
		}
		vals := make([]cty.Value, len(val))
		for i, item := range val {
			vals[i] = goToCtyValue(item)
		}
		return cty.TupleVal(vals)
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	default:
		return cty.StringVal(fmt.Sprintf("%v", v))
	}
}

// goJSONToCty parses a JSON string into a cty.Value
func goJSONToCty(jsonStr string) cty.Value {
	var v any
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return cty.StringVal(jsonStr)
	}
	return goToCtyValue(v)
}

