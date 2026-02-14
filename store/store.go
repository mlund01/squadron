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
	Missions MissionStore
	Datasets DatasetStore
	Sessions SessionStore
	closer   func() error
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
	StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, outputJSON string) error
}

// MissionTask represents a task within a mission run
type MissionTask struct {
	ID         string     `json:"id"`
	MissionID  string     `json:"missionId"`
	TaskName   string     `json:"taskName"`
	Status     string     `json:"status"`
	ConfigJSON string     `json:"configJson"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	OutputJSON *string    `json:"outputJson,omitempty"`
	Error      *string    `json:"error,omitempty"`
}

// SessionStore tracks agent/commander sessions and their message history
type SessionStore interface {
	CreateSession(taskID, role, agentName, model string) (id string, err error)
	CompleteSession(id string, err error)
	AppendMessage(sessionID, role, content string) error
	GetMessages(sessionID string) ([]SessionMessage, error)
	GetSessionsByTask(taskID string) ([]SessionInfo, error)
	StoreToolResult(taskID, sessionID, toolName, inputParams, rawData string) error
}

// SessionInfo describes a session
type SessionInfo struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Role      string    `json:"role"`
	AgentName string    `json:"agentName,omitempty"`
	Model     string    `json:"model,omitempty"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
}

// SessionMessage represents a single message in a session
type SessionMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

// DatasetInfo describes a dataset (defined here to avoid import cycles with aitools)
type DatasetInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ItemCount   int    `json:"itemCount"`
}

// DatasetStore manages datasets and their items
type DatasetStore interface {
	CreateDataset(missionID, name, description string) (id string, err error)
	AddItems(datasetID string, items []cty.Value) error
	SetItems(datasetID string, items []cty.Value) error // Replace all items
	GetItems(datasetID string, offset, limit int) ([]cty.Value, error)
	GetItemCount(datasetID string) (int, error)
	GetSample(datasetID string, count int) ([]cty.Value, error)
	GetDatasetByName(missionID, name string) (id string, err error)
	ListDatasets(missionID string) ([]DatasetInfo, error)
}

