package runtime

// RunState is the durable lifecycle state of a mission run.
type RunState string

const (
	RunPending   RunState = "pending"
	RunRunning   RunState = "running"
	RunWaiting   RunState = "waiting"
	RunStopping  RunState = "stopping"
	RunStopped   RunState = "stopped"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
)

// CommanderState is the lifecycle state of the single commander session that
// belongs to a mission run.
type CommanderState string

const (
	CommanderInitializing CommanderState = "initializing"
	CommanderActive       CommanderState = "active"
	CommanderWaiting      CommanderState = "waiting"
	CommanderStopping     CommanderState = "stopping"
	CommanderStopped      CommanderState = "stopped"
	CommanderCompleted    CommanderState = "completed"
	CommanderFailed       CommanderState = "failed"
)

// TaskState is the durable lifecycle state of one declared task instance.
type TaskState string

const (
	TaskBlocked      TaskState = "blocked"
	TaskReady        TaskState = "ready"
	TaskDispatching  TaskState = "dispatching"
	TaskRunning      TaskState = "running"
	TaskWaiting      TaskState = "waiting"
	TaskRetryPending TaskState = "retry_pending"
	TaskCompleted    TaskState = "completed"
	TaskFailed       TaskState = "failed"
	TaskSkipped      TaskState = "skipped"
	TaskCanceled     TaskState = "canceled"
)

// AttemptState is the durable lifecycle state of one immutable task attempt.
type AttemptState string

const (
	AttemptCreated   AttemptState = "created"
	AttemptRunning   AttemptState = "running"
	AttemptWaiting   AttemptState = "waiting"
	AttemptCompleted AttemptState = "completed"
	AttemptFailed    AttemptState = "failed"
	AttemptTimedOut  AttemptState = "timed_out"
	AttemptCanceled  AttemptState = "canceled"
)

var runTransitions = map[RunState]map[RunState]struct{}{
	RunPending:  states(RunRunning, RunFailed),
	RunRunning:  states(RunWaiting, RunStopping, RunCompleted, RunFailed),
	RunWaiting:  states(RunRunning, RunStopping, RunFailed),
	RunStopping: states(RunStopped, RunFailed),
	RunStopped:  states(RunRunning),
}

var commanderTransitions = map[CommanderState]map[CommanderState]struct{}{
	CommanderInitializing: states(CommanderActive, CommanderFailed),
	CommanderActive:       states(CommanderWaiting, CommanderStopping, CommanderCompleted, CommanderFailed),
	CommanderWaiting:      states(CommanderActive, CommanderStopping, CommanderFailed),
	CommanderStopping:     states(CommanderStopped, CommanderFailed),
	CommanderStopped:      states(CommanderActive),
}

var taskTransitions = map[TaskState]map[TaskState]struct{}{
	TaskBlocked:      states(TaskReady, TaskSkipped, TaskCanceled),
	TaskReady:        states(TaskDispatching, TaskSkipped, TaskCanceled),
	TaskDispatching:  states(TaskRunning, TaskReady, TaskFailed, TaskCanceled),
	TaskRunning:      states(TaskWaiting, TaskRetryPending, TaskCompleted, TaskFailed, TaskCanceled),
	TaskWaiting:      states(TaskRunning, TaskRetryPending, TaskFailed, TaskCanceled),
	TaskRetryPending: states(TaskReady, TaskFailed, TaskCanceled),
}

var attemptTransitions = map[AttemptState]map[AttemptState]struct{}{
	AttemptCreated: states(AttemptRunning, AttemptCanceled),
	AttemptRunning: states(AttemptWaiting, AttemptCompleted, AttemptFailed, AttemptTimedOut, AttemptCanceled),
	AttemptWaiting: states(AttemptRunning, AttemptFailed, AttemptTimedOut, AttemptCanceled),
}

// CanTransitionRun reports whether a run transition is permitted.
func CanTransitionRun(from, to RunState) bool {
	return canTransition(runTransitions, from, to)
}

// CanTransitionCommander reports whether a commander-session transition is permitted.
func CanTransitionCommander(from, to CommanderState) bool {
	return canTransition(commanderTransitions, from, to)
}

// CanTransitionTask reports whether a task-instance transition is permitted.
func CanTransitionTask(from, to TaskState) bool {
	return canTransition(taskTransitions, from, to)
}

// CanTransitionAttempt reports whether a task-attempt transition is permitted.
func CanTransitionAttempt(from, to AttemptState) bool {
	return canTransition(attemptTransitions, from, to)
}

func states[T comparable](values ...T) map[T]struct{} {
	result := make(map[T]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func canTransition[T comparable](transitions map[T]map[T]struct{}, from, to T) bool {
	_, ok := transitions[from][to]
	return ok
}
