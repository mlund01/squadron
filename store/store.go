package store

import (
	"time"

	"github.com/zclconf/go-cty/cty"
)

// Bundle holds all stores for tracking mission execution.
// Note: ResultStore and KnowledgeStore remain in their original packages
// (aitools and mission) to avoid import cycles. They continue to be used
// directly by the runner and can be backed by SQLite separately.
type Bundle struct {
	Questions QuestionStore
	Missions  MissionStore
	Datasets  DatasetStore
	Sessions  SessionStore
	closer    func() error
}

// Close cleans up the bundle resources
func (b *Bundle) Close() error {
	if b.closer != nil {
		return b.closer()
	}
	return nil
}

// MissionStore tracks mission runs and their tasks
type MissionStore interface {
	CreateMission(name string, inputsJSON, configJSON string) (id string, err error)
	UpdateMissionStatus(id, status string) error
	CreateTask(missionID, taskName, configJSON string) (id string, err error)
	UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error
	GetTasksByMission(missionID string) ([]MissionTask, error)
}

// MissionTask represents a task within a mission run
type MissionTask struct {
	ID         string
	MissionID  string
	TaskName   string
	Status     string // pending, running, completed, failed
	ConfigJSON string
	StartedAt  *time.Time
	FinishedAt *time.Time
	OutputJSON *string
	Error      *string
}

// SessionStore tracks agent/commander sessions and their message history
type SessionStore interface {
	CreateSession(taskID, role, agentName, model string) (id string, err error)
	CompleteSession(id string, err error)
	AppendMessage(sessionID, role, content string) error
	GetMessages(sessionID string) ([]SessionMessage, error)
	GetSessionsByTask(taskID string) ([]SessionInfo, error)
}

// SessionInfo describes a session
type SessionInfo struct {
	ID        string
	TaskID    string
	Role      string // "commander" or "agent"
	AgentName string
	Model     string
	Status    string
	StartedAt time.Time
}

// SessionMessage represents a single message in a session
type SessionMessage struct {
	ID        int
	Role      string // "system", "user", "assistant", "tool_use", "tool_result"
	Content   string
	CreatedAt time.Time
}

// DatasetInfo describes a dataset (defined here to avoid import cycles with aitools)
type DatasetInfo struct {
	Name        string
	Description string
	ItemCount   int
}

// DatasetStore manages datasets and their items
type DatasetStore interface {
	CreateDataset(missionID, name, description string) (id string, err error)
	AddItems(datasetID string, items []cty.Value) error
	GetItems(datasetID string, offset, limit int) ([]cty.Value, error)
	GetItemCount(datasetID string) (int, error)
	GetSample(datasetID string, count int) ([]cty.Value, error)
	GetDatasetByName(missionID, name string) (id string, err error)
	ListDatasets(missionID string) ([]DatasetInfo, error)
	UpdateItemStatus(datasetID string, index int, status string, outputJSON, errMsg *string) error
}

// QuestionStore handles commander question deduplication across iterations
type QuestionStore interface {
	StoreQuestion(taskID, iterationKey, question string) (id string, err error)
	SetAnswer(id, answer string) error
	GetAnswer(id string) (answer string, ready bool, err error)
	ListQuestions(taskID, excludeIterationKey string) ([]QuestionInfo, error)
}

// QuestionInfo describes a stored question
type QuestionInfo struct {
	ID        string
	Question  string
	HasAnswer bool
}
