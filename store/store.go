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
	Events   EventStore
	Costs    CostStore
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
	// UpdateMissionStatusCAS atomically transitions a mission status, returning false if current status doesn't match expected.
	UpdateMissionStatusCAS(id, expectedOldStatus, newStatus string) (bool, error)
	CreateTask(missionID, taskName, configJSON string) (id string, err error)
	UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error
	// UpdateTaskStatusCAS atomically transitions a task status, returning false if current status doesn't match expected.
	UpdateTaskStatusCAS(id, expectedOldStatus, newStatus string, outputJSON, errMsg *string) (bool, error)
	GetTask(id string) (*MissionTask, error)
	GetTasksByMission(missionID string) ([]MissionTask, error)
	GetTaskByName(missionID, taskName string) (*MissionTask, error)
	GetMission(id string) (*MissionRecord, error)
	ListMissions(limit, offset int) ([]MissionRecord, int, error)
	StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, itemID *string, outputJSON string) error
	GetTaskOutputs(taskID string) ([]TaskOutputRow, error)

	// Task inputs (per-execution/iteration resolved inputs)
	StoreTaskInput(taskID string, iterationIndex *int, objective string) error
	GetTaskInputs(taskID string) ([]TaskInput, error)

	// Subtask management
	SetSubtasks(taskID, sessionID string, iterationIndex *int, titles []string) error
	GetSubtasks(taskID, sessionID string, iterationIndex *int) ([]Subtask, error)
	GetSubtasksByTask(taskID string) ([]Subtask, error)
	CompleteSubtask(taskID, sessionID string, iterationIndex *int) error

	// Route decisions
	StoreRouteDecision(missionID, routerTask, targetTask, condition string) error
	GetRouteDecisions(missionID string) ([]RouteDecision, error)
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

// MissionRecord represents a mission row
type MissionRecord struct {
	ID              string     `json:"id"`
	MissionName     string     `json:"missionName"`
	Status          string     `json:"status"`
	InputValuesJSON string     `json:"inputValuesJson"`
	ConfigJSON      string     `json:"configJson"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
}

// TaskOutputRow represents a single row from the task_outputs table
type TaskOutputRow struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"taskId"`
	DatasetName  *string   `json:"datasetName,omitempty"`
	DatasetIndex *int      `json:"datasetIndex,omitempty"`
	ItemID       *string   `json:"itemId,omitempty"`
	OutputJSON   string    `json:"outputJson"`
	CreatedAt    time.Time `json:"createdAt"`
}

// TaskInput represents a resolved input for a task execution (or one iteration of it)
type TaskInput struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"taskId"`
	IterationIndex *int      `json:"iterationIndex,omitempty"`
	Objective      string    `json:"objective"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Subtask represents a planned step within a task execution
type Subtask struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"taskId"`
	SessionID      string     `json:"sessionId"`
	IterationIndex *int       `json:"iterationIndex,omitempty"`
	Index          int        `json:"index"`
	Title          string     `json:"title"`
	Status         string     `json:"status"` // pending, in_progress, completed
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

// RouteDecision represents a routing decision made during mission execution
type RouteDecision struct {
	ID            string    `json:"id"`
	MissionID     string    `json:"missionId"`
	RouterTask    string    `json:"routerTask"`
	TargetTask    string    `json:"targetTask"`
	ConditionText string    `json:"conditionText"`
	CreatedAt     time.Time `json:"createdAt"`
}

// SessionStore tracks agent/commander sessions and their message history
type SessionStore interface {
	CreateSession(taskID, role, agentName, model string, iterationIndex *int) (id string, err error)
	CompleteSession(id string, err error)
	ReopenSession(id string)
	AppendMessage(sessionID, role, content string, createdAt, completedAt time.Time) error
	GetMessages(sessionID string) ([]SessionMessage, error)
	GetSessionsByTask(taskID string) ([]SessionInfo, error)
	StoreToolResult(taskID, sessionID, toolCallId, toolName, inputParams, rawData string, startedAt, finishedAt time.Time) error
	// StartToolCall records a tool call before execution (status=started). Returns a record ID.
	StartToolCall(taskID, sessionID, toolCallId, toolName, inputParams string) (string, error)
	// CompleteToolCall marks a tool call as completed with result data.
	CompleteToolCall(id, rawData string) error
	GetToolResultsByTask(taskID string) ([]ToolResult, error)

	// Chat-specific methods
	CreateChatSession(agentName, model string) (string, error)
	ListChatSessions(agentName string, limit, offset int) ([]SessionInfo, int, error)
}

// SessionInfo describes a session
type SessionInfo struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"taskId,omitempty"`
	Role           string     `json:"role"`
	AgentName      string     `json:"agentName,omitempty"`
	Model          string     `json:"model,omitempty"`
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	IterationIndex *int       `json:"iterationIndex,omitempty"`
}

// ToolResult represents a stored tool call with timing
type ToolResult struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"taskId"`
	SessionID   string    `json:"sessionId"`
	ToolCallId  string    `json:"toolCallId"`
	ToolName    string    `json:"toolName"`
	InputParams string    `json:"inputParams"`
	RawData     string    `json:"rawData"`
	Status      string    `json:"status"` // "started", "completed", "interrupted"
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
}

// SessionMessage represents a single message in a session
type SessionMessage struct {
	ID          int       `json:"id"`
	Role        string    `json:"role"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"createdAt"`
	CompletedAt time.Time `json:"completedAt"`
}

// DatasetInfo describes a dataset (defined here to avoid import cycles with aitools)
type DatasetInfo struct {
	ID          string `json:"id"`
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
	GetItemsRaw(datasetID string, offset, limit int) ([]string, error)
	LockDataset(datasetID string) error
	IsDatasetLocked(datasetID string) (bool, error)
}

// EventStore persists mission execution events for history/audit
type EventStore interface {
	StoreEvent(event MissionEvent) error
	StoreEvents(events []MissionEvent) error
	GetEventsByMission(missionID string, limit, offset int) ([]MissionEvent, error)
	GetEventsByTask(taskID string, limit, offset int) ([]MissionEvent, error)
}

// MissionEvent represents a single event during mission execution
type MissionEvent struct {
	ID             string    `json:"id"`
	MissionID      string    `json:"missionId"`
	TaskID         *string   `json:"taskId,omitempty"`
	SessionID      *string   `json:"sessionId,omitempty"`
	IterationIndex *int      `json:"iterationIndex,omitempty"`
	EventType      string    `json:"eventType"`
	DataJSON       string    `json:"dataJson"`
	CreatedAt      time.Time `json:"createdAt"`
}

// CostStore tracks per-turn costs for aggregation and reporting.
type CostStore interface {
	// StoreTurnCost records cost data for a single LLM turn.
	StoreTurnCost(cost TurnCostRecord) error
	// GetCostsByMission returns all turn costs for a mission.
	GetCostsByMission(missionID string) ([]TurnCostRecord, error)
	// GetCostSummary returns aggregated costs within a time range, grouped by the specified field.
	GetCostSummary(from, to time.Time, groupBy string) ([]CostSummaryRow, error)
	// GetRecentMissionCosts returns total cost per mission for recent missions.
	GetRecentMissionCosts(limit int) ([]MissionCostRow, error)
	// GetCostsByDateAndField returns costs grouped by date and a secondary field (model or mission_name).
	GetCostsByDateAndField(from, to time.Time, field string) ([]DateFieldCostRow, error)
	// GetTotalCosts returns overall totals within a time range.
	GetTotalCosts(from, to time.Time) (*CostTotals, error)
}

// TurnCostRecord represents a single turn's cost data stored in the DB.
type TurnCostRecord struct {
	ID               string    `json:"id"`
	MissionID        string    `json:"missionId"`
	TaskID           string    `json:"taskId"`
	SessionID        string    `json:"sessionId"`
	MissionName      string    `json:"missionName"`
	TaskName         string    `json:"taskName"`
	Entity           string    `json:"entity"`
	Model            string    `json:"model"`
	InputTokens      int       `json:"inputTokens"`
	OutputTokens     int       `json:"outputTokens"`
	CacheWriteTokens int       `json:"cacheWriteTokens"`
	CacheReadTokens  int       `json:"cacheReadTokens"`
	InputCost        float64   `json:"inputCost"`
	OutputCost       float64   `json:"outputCost"`
	CacheReadCost    float64   `json:"cacheReadCost"`
	CacheWriteCost   float64   `json:"cacheWriteCost"`
	TotalCost        float64   `json:"totalCost"`
	DurationMs       int64     `json:"durationMs"`
	CreatedAt        time.Time `json:"createdAt"`
}

// CostSummaryRow is an aggregated cost row grouped by a field (model, mission, date, etc.)
type CostSummaryRow struct {
	GroupKey       string  `json:"groupKey"`
	Turns          int     `json:"turns"`
	TotalCost      float64 `json:"totalCost"`
	InputCost      float64 `json:"inputCost"`
	OutputCost     float64 `json:"outputCost"`
	CacheReadCost  float64 `json:"cacheReadCost"`
	CacheWriteCost float64 `json:"cacheWriteCost"`
}

// MissionCostRow represents total cost for a single mission run.
type MissionCostRow struct {
	MissionID   string    `json:"missionId"`
	MissionName string    `json:"missionName"`
	Status      string    `json:"status"`
	Turns       int       `json:"turns"`
	TotalCost   float64   `json:"totalCost"`
	StartedAt   time.Time `json:"startedAt"`
}

// DateFieldCostRow is a cost row grouped by date + a secondary field (model or mission).
type DateFieldCostRow struct {
	Date      string  `json:"date"`
	FieldKey  string  `json:"fieldKey"`
	TotalCost float64 `json:"totalCost"`
}

// CostTotals holds overall cost aggregates.
type CostTotals struct {
	TotalCost        float64 `json:"totalCost"`
	InputCost        float64 `json:"inputCost"`
	OutputCost       float64 `json:"outputCost"`
	CacheReadCost    float64 `json:"cacheReadCost"`
	CacheWriteCost   float64 `json:"cacheWriteCost"`
	TotalTurns       int     `json:"totalTurns"`
	TotalInputTokens int     `json:"totalInputTokens"`
	TotalOutputTokens int    `json:"totalOutputTokens"`
}

