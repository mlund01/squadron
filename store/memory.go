package store

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// NewMemoryBundle creates a Bundle backed entirely by in-memory stores
func NewMemoryBundle() *Bundle {
	return &Bundle{
		Missions: &MemoryMissionStore{tasks: make(map[string]*MissionTask)},
		Datasets: &MemoryDatasetStore{datasets: make(map[string]*memDataset)},
		Sessions: &MemorySessionStore{sessions: make(map[string]*memSession)},
	}
}

// =============================================================================
// MemoryMissionStore
// =============================================================================

type MemoryMissionStore struct {
	mu    sync.Mutex
	tasks map[string]*MissionTask
}

func (s *MemoryMissionStore) CreateMission(name string, inputsJSON, configJSON string) (string, error) {
	return generateID(), nil
}

func (s *MemoryMissionStore) UpdateMissionStatus(id, status string) error {
	return nil
}

func (s *MemoryMissionStore) CreateTask(missionID, taskName, configJSON string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	now := time.Now()
	s.tasks[id] = &MissionTask{
		ID:        id,
		MissionID: missionID,
		TaskName:  taskName,
		Status:    "pending",
		StartedAt: &now,
	}
	return id, nil
}

func (s *MemoryMissionStore) UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if task, ok := s.tasks[id]; ok {
		task.Status = status
		task.OutputJSON = outputJSON
		task.Error = errMsg
		if status == "completed" || status == "failed" {
			now := time.Now()
			task.FinishedAt = &now
		}
	}
	return nil
}

func (s *MemoryMissionStore) GetTasksByMission(missionID string) ([]MissionTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var tasks []MissionTask
	for _, t := range s.tasks {
		if t.MissionID == missionID {
			tasks = append(tasks, *t)
		}
	}
	return tasks, nil
}

func (s *MemoryMissionStore) StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, outputJSON string) error {
	return nil // no-op for memory store
}

// =============================================================================
// MemorySessionStore
// =============================================================================

type memSession struct {
	info     SessionInfo
	messages []SessionMessage
}

type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]*memSession
}

func (s *MemorySessionStore) CreateSession(taskID, role, agentName, model string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	s.sessions[id] = &memSession{
		info: SessionInfo{
			ID:        id,
			TaskID:    taskID,
			Role:      role,
			AgentName: agentName,
			Model:     model,
			Status:    "running",
			StartedAt: time.Now(),
		},
	}
	return id, nil
}

func (s *MemorySessionStore) CompleteSession(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[id]; ok {
		if err != nil {
			sess.info.Status = "failed"
		} else {
			sess.info.Status = "completed"
		}
	}
}

func (s *MemorySessionStore) AppendMessage(sessionID, role, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	sess.messages = append(sess.messages, SessionMessage{
		ID:        len(sess.messages) + 1,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	})
	return nil
}

func (s *MemorySessionStore) GetMessages(sessionID string) ([]SessionMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	msgs := make([]SessionMessage, len(sess.messages))
	copy(msgs, sess.messages)
	return msgs, nil
}

func (s *MemorySessionStore) GetSessionsByTask(taskID string) ([]SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sessions []SessionInfo
	for _, sess := range s.sessions {
		if sess.info.TaskID == taskID {
			sessions = append(sessions, sess.info)
		}
	}
	return sessions, nil
}

func (s *MemorySessionStore) StoreToolResult(taskID, sessionID, toolName, inputParams, rawData string) error {
	return nil // no-op for memory store
}

// =============================================================================
// MemoryDatasetStore
// =============================================================================

type memDataset struct {
	id          string
	missionID   string
	name        string
	description string
	items       []memDatasetItem
}

type memDatasetItem struct {
	item cty.Value
}

type MemoryDatasetStore struct {
	mu       sync.Mutex
	datasets map[string]*memDataset // keyed by dataset ID
}

func (s *MemoryDatasetStore) CreateDataset(missionID, name, description string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	s.datasets[id] = &memDataset{
		id:          id,
		missionID:   missionID,
		name:        name,
		description: description,
	}
	return id, nil
}

func (s *MemoryDatasetStore) AddItems(datasetID string, items []cty.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[datasetID]
	if !ok {
		return fmt.Errorf("dataset %s not found", datasetID)
	}

	for _, item := range items {
		ds.items = append(ds.items, memDatasetItem{item: item})
	}
	return nil
}

func (s *MemoryDatasetStore) SetItems(datasetID string, items []cty.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[datasetID]
	if !ok {
		return fmt.Errorf("dataset %s not found", datasetID)
	}

	ds.items = make([]memDatasetItem, len(items))
	for i, item := range items {
		ds.items[i] = memDatasetItem{item: item}
	}
	return nil
}

func (s *MemoryDatasetStore) GetItems(datasetID string, offset, limit int) ([]cty.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[datasetID]
	if !ok {
		return nil, fmt.Errorf("dataset %s not found", datasetID)
	}

	if offset >= len(ds.items) {
		return nil, nil
	}

	end := offset + limit
	if end > len(ds.items) {
		end = len(ds.items)
	}

	result := make([]cty.Value, end-offset)
	for i, di := range ds.items[offset:end] {
		result[i] = di.item
	}
	return result, nil
}

func (s *MemoryDatasetStore) GetItemCount(datasetID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[datasetID]
	if !ok {
		return 0, fmt.Errorf("dataset %s not found", datasetID)
	}
	return len(ds.items), nil
}

func (s *MemoryDatasetStore) GetSample(datasetID string, count int) ([]cty.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ds, ok := s.datasets[datasetID]
	if !ok {
		return nil, fmt.Errorf("dataset %s not found", datasetID)
	}

	if count >= len(ds.items) {
		result := make([]cty.Value, len(ds.items))
		for i, di := range ds.items {
			result[i] = di.item
		}
		return result, nil
	}

	// Random sample
	indices := rand.Perm(len(ds.items))[:count]
	result := make([]cty.Value, count)
	for i, idx := range indices {
		result[i] = ds.items[idx].item
	}
	return result, nil
}

func (s *MemoryDatasetStore) GetDatasetByName(missionID, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ds := range s.datasets {
		if ds.missionID == missionID && ds.name == name {
			return ds.id, nil
		}
	}
	return "", fmt.Errorf("dataset '%s' not found for mission %s", name, missionID)
}

func (s *MemoryDatasetStore) ListDatasets(missionID string) ([]DatasetInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var infos []DatasetInfo
	for _, ds := range s.datasets {
		if ds.missionID == missionID {
			infos = append(infos, DatasetInfo{
				Name:        ds.name,
				Description: ds.description,
				ItemCount:   len(ds.items),
			})
		}
	}
	return infos, nil
}

// =============================================================================
// Helpers
// =============================================================================

func generateID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
