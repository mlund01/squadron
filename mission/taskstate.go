package mission

import (
	"fmt"
	"sync"
)

// TaskState represents the lifecycle state of a task.
type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskReady     TaskState = "ready"
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskStopping  TaskState = "stopping"
	TaskStopped   TaskState = "stopped"
)

// MissionState represents the lifecycle state of a mission.
type MissionState string

const (
	MissionPending   MissionState = "pending"
	MissionRunning   MissionState = "running"
	MissionCompleted MissionState = "completed"
	MissionFailed    MissionState = "failed"
	MissionStopping  MissionState = "stopping"
	MissionStopped   MissionState = "stopped"
)

// validTaskTransitions defines allowed state transitions.
var validTaskTransitions = map[TaskState][]TaskState{
	TaskPending:  {TaskReady},
	TaskReady:    {TaskRunning},
	TaskRunning:  {TaskCompleted, TaskFailed, TaskStopping},
	TaskStopping: {TaskStopped},
	TaskStopped:  {TaskReady},   // resume
	TaskFailed:   {TaskReady},   // retry
	// TaskCompleted is terminal
}

var validMissionTransitions = map[MissionState][]MissionState{
	MissionPending:  {MissionRunning},
	MissionRunning:  {MissionCompleted, MissionFailed, MissionStopping},
	MissionStopping: {MissionStopped},
	MissionStopped:  {MissionRunning}, // resume
	// MissionCompleted, MissionFailed are terminal
}

// TaskStateStore persists task/mission state transitions.
// Implementations should use compare-and-swap to prevent concurrent double-transitions.
type TaskStateStore interface {
	// UpdateTaskStateCAS atomically transitions a task from expectedOld to newState.
	// Returns false if the current state doesn't match expectedOld.
	UpdateTaskStateCAS(taskID string, expectedOld, newState TaskState, outputJSON, errMsg *string) (bool, error)

	// UpdateMissionStateCAS atomically transitions a mission from expectedOld to newState.
	UpdateMissionStateCAS(missionID string, expectedOld, newState MissionState) (bool, error)
}

// StateTransitionCallback is called after a successful state transition.
type StateTransitionCallback func(taskName string, from, to TaskState)

// TaskStateManager is the single authority for task lifecycle state.
// All state reads and mutations go through this manager.
type TaskStateManager struct {
	mu           sync.RWMutex
	tasks        map[string]TaskState  // taskName → current state
	taskIDs      map[string]string     // taskName → DB task ID
	missionID    string
	missionState MissionState
	store        TaskStateStore
	onTransition StateTransitionCallback
}

// NewTaskStateManager creates a new state manager.
func NewTaskStateManager(missionID string, store TaskStateStore) *TaskStateManager {
	return &TaskStateManager{
		tasks:        make(map[string]TaskState),
		taskIDs:      make(map[string]string),
		missionID:    missionID,
		missionState: MissionPending,
		store:        store,
	}
}

// OnTransition registers a callback fired after each task state transition.
func (m *TaskStateManager) OnTransition(fn StateTransitionCallback) {
	m.onTransition = fn
}

// RegisterTask adds a task with an initial state and its DB ID.
func (m *TaskStateManager) RegisterTask(taskName, taskID string, initialState TaskState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[taskName] = initialState
	m.taskIDs[taskName] = taskID
}

// SetTaskID updates the DB ID for a task (called after DB record creation on fresh runs).
func (m *TaskStateManager) SetTaskID(taskName, taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskIDs[taskName] = taskID
}

// TaskState returns the current state of a task.
func (m *TaskStateManager) GetTaskState(taskName string) (TaskState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.tasks[taskName]
	return s, ok
}

// IsCompleted returns true if the task has reached TaskCompleted.
func (m *TaskStateManager) IsCompleted(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[taskName] == TaskCompleted
}

// IsTerminal returns true if the task is in a terminal state (completed or failed).
func (m *TaskStateManager) IsTerminal(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.tasks[taskName]
	return s == TaskCompleted || s == TaskFailed
}

// IsInFlight returns true if the task is currently running or stopping.
func (m *TaskStateManager) IsInFlight(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.tasks[taskName]
	return s == TaskRunning || s == TaskStopping
}

// AllCompleted returns true if every registered task is in TaskCompleted state.
func (m *TaskStateManager) AllCompleted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.tasks {
		if s != TaskCompleted {
			return false
		}
	}
	return true
}

// AnyInFlight returns true if any task is running or stopping.
func (m *TaskStateManager) AnyInFlight() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.tasks {
		if s == TaskRunning || s == TaskStopping {
			return true
		}
	}
	return false
}

// TransitionTask moves a task to a new state.
// Validates the transition, persists to the store (write-ahead), then updates in-memory state.
func (m *TaskStateManager) TransitionTask(taskName string, to TaskState, outputJSON, errMsg *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	from, ok := m.tasks[taskName]
	if !ok {
		return fmt.Errorf("task %q not registered", taskName)
	}

	if !isValidTaskTransition(from, to) {
		return fmt.Errorf("invalid task transition %q: %s → %s", taskName, from, to)
	}

	// Write-ahead: persist to store before updating in-memory state
	// Skip DB write if task has no DB ID yet (fresh run — DB record created in runTask)
	if m.store != nil {
		taskID := m.taskIDs[taskName]
		if taskID != "" {
			ok, err := m.store.UpdateTaskStateCAS(taskID, from, to, outputJSON, errMsg)
			if err != nil {
				return fmt.Errorf("persisting task transition %q %s→%s: %w", taskName, from, to, err)
			}
			if !ok {
				return fmt.Errorf("task %q state conflict: expected %s in DB", taskName, from)
			}
		}
	}

	// Update in-memory state
	m.tasks[taskName] = to

	if m.onTransition != nil {
		m.onTransition(taskName, from, to)
	}

	return nil
}

// TransitionMission moves the mission to a new state.
func (m *TaskStateManager) TransitionMission(to MissionState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := m.missionState
	if !isValidMissionTransition(from, to) {
		return fmt.Errorf("invalid mission transition: %s → %s", from, to)
	}

	if m.store != nil {
		ok, err := m.store.UpdateMissionStateCAS(m.missionID, from, to)
		if err != nil {
			return fmt.Errorf("persisting mission transition %s→%s: %w", from, to, err)
		}
		if !ok {
			return fmt.Errorf("mission state conflict: expected %s in DB", from)
		}
	}

	m.missionState = to
	return nil
}

// GetMissionState returns the current mission state.
func (m *TaskStateManager) GetMissionState() MissionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.missionState
}

// StopAll transitions all running tasks to stopping.
func (m *TaskStateManager) StopAll() {
	m.mu.Lock()
	names := make([]string, 0)
	for name, state := range m.tasks {
		if state == TaskRunning {
			names = append(names, name)
		}
	}
	m.mu.Unlock()

	for _, name := range names {
		// Best-effort — ignore errors from already-transitioned tasks
		_ = m.TransitionTask(name, TaskStopping, nil, nil)
	}
}

// Snapshot returns a copy of all task states for debugging/display.
func (m *TaskStateManager) Snapshot() map[string]TaskState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap := make(map[string]TaskState, len(m.tasks))
	for k, v := range m.tasks {
		snap[k] = v
	}
	return snap
}

func isValidTaskTransition(from, to TaskState) bool {
	allowed, ok := validTaskTransitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}
	return false
}

func isValidMissionTransition(from, to MissionState) bool {
	allowed, ok := validMissionTransitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}
	return false
}
