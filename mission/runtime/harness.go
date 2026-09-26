package runtime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"squadron/store"
)

// TaskDefinition is the graph information the harness needs to determine
// eligibility. Execution policy is resolved after a dispatch is accepted.
type TaskDefinition struct {
	Name              string
	Objective         string
	DependsOn         []string
	SendTo            []string
	Routes            []RouteDefinition
	MaxRetries        int
	RetryDelaySeconds int
	ResultPolicy      ResultPolicy
	Iterator          *IterationPolicy
	ParentTask        string
}

type RouteDefinition struct {
	Target    string                 `json:"target"`
	Condition string                 `json:"condition"`
	IsMission bool                   `json:"isMission,omitempty"`
	Inputs    []RouteInputDefinition `json:"inputs,omitempty"`
}

// RouteInputDefinition is the non-secret portion of a destination mission's
// input contract made available to the commander and durable harness.
type RouteInputDefinition struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

type RouteRequest struct {
	RequestID string
	TaskName  string
	Target    string
	Inputs    map[string]string
	Revision  uint64
}

type RouteSelection struct {
	RequestID string
	TaskName  string
	Target    string
	Inputs    map[string]string
	IsMission bool
	Revision  uint64
}

const (
	CrossMissionLaunchPending  = "pending"
	CrossMissionLaunchLaunched = "launched"
)

// CrossMissionLaunch is the durable request/result boundary between a parent
// route selection and creation of the child run.
type CrossMissionLaunch struct {
	ID         string            `json:"id"`
	TaskName   string            `json:"taskName"`
	Target     string            `json:"target"`
	Inputs     map[string]string `json:"inputs,omitempty"`
	State      string            `json:"state"`
	ChildRunID string            `json:"childRunId,omitempty"`
}

type InspectTaskRequest struct {
	RequestID string
	TaskName  string
	Revision  uint64
}

const maxTaskClarifications = 3

// ClarifyTaskRequest asks the same logical task-worker session a bounded,
// read-only follow-up. The request is persisted before provider work begins.
type ClarifyTaskRequest struct {
	RequestID string `json:"requestId"`
	MessageID string `json:"messageId"`
	TaskName  string `json:"taskName"`
	Question  string `json:"question"`
	Revision  uint64 `json:"revision"`
}

// TaskClarification is the durable supplemental evidence returned by a
// restored task-worker session. Tools are unavailable during this turn.
type TaskClarification struct {
	RequestID string              `json:"requestId"`
	TaskName  string              `json:"taskName"`
	Question  string              `json:"question"`
	Answer    []store.MessagePart `json:"answer"`
}

// TaskClarificationResult is the executor response plus the updated exact
// session transcript retained for a later clarification or replay.
type TaskClarificationResult struct {
	Answer            []store.MessagePart  `json:"answer"`
	SessionTranscript []TaskSessionMessage `json:"sessionTranscript"`
}

// TaskInspection is the complete accepted task output exposed to the
// commander. It deliberately excludes worker turns, reasoning, tool calls, and
// provider-raw output.
type TaskInspection struct {
	RequestID   string              `json:"requestId"`
	TaskName    string              `json:"taskName"`
	Status      ResultStatus        `json:"status"`
	Summary     string              `json:"summary"`
	Content     []store.MessagePart `json:"content,omitempty"`
	Artifacts   []ArtifactReference `json:"artifacts,omitempty"`
	Data        json.RawMessage     `json:"data,omitempty"`
	Diagnostics json.RawMessage     `json:"diagnostics,omitempty"`
	Provenance  ResultProvenance    `json:"provenance"`
}

// TaskFailure is the latest durable failure observed for a task attempt.
// Error is intentionally operational metadata; it is not automatically placed
// in model-visible context.
type TaskFailure struct {
	AttemptID string
	Attempt   int
	Error     string
}

// CommanderSession identifies the one commander conversation for a run.
type CommanderSession struct {
	ID    string
	State CommanderState
}

// CommanderMessage is one immutable message in the run-scoped commander
// conversation.
type CommanderMessage struct {
	ID        string
	Role      string
	Parts     []store.MessagePart
	Telemetry *ModelTurnTelemetry
}

// ModelTurnTelemetry is the provider-neutral accounting record for one
// completed model response. ID is stable within a mission run, so downstream
// event, cost, and UI projections can upsert it without double-counting after
// restart replay.
type ModelTurnTelemetry struct {
	ID               string `json:"id"`
	SessionID        string `json:"sessionId"`
	Entity           string `json:"entity"`
	TaskName         string `json:"taskName,omitempty"`
	Model            string `json:"model,omitempty"`
	FinishReason     string `json:"finishReason,omitempty"`
	InputTokens      int    `json:"inputTokens,omitempty"`
	OutputTokens     int    `json:"outputTokens,omitempty"`
	CacheWriteTokens int    `json:"cacheWriteTokens,omitempty"`
	CacheReadTokens  int    `json:"cacheReadTokens,omitempty"`
}

// TaskSnapshot is the read-only task projection supplied to the commander.
type TaskSnapshot struct {
	Name    string
	State   TaskState
	Attempt int
}

// TaskResultSummary is the bounded projection exposed in commander snapshots.
// Full rich content remains behind TaskResult for explicit inspection.
type TaskResultSummary struct {
	Status        ResultStatus
	Summary       string
	ArtifactCount int
	HasData       bool
}

// Snapshot is a revisioned, read-only projection of harness state.
type Snapshot struct {
	RunID                 string
	RunState              RunState
	Commander             CommanderSession
	Revision              uint64
	Ready                 []string
	Tasks                 map[string]TaskSnapshot
	Active                []Dispatch
	Results               map[string]TaskResultSummary
	Failures              map[string]TaskFailure
	PendingRoutes         map[string][]RouteDefinition
	RouteSelections       map[string]RouteSelection
	CrossMissionLaunches  map[string]CrossMissionLaunch
	PendingClarifications []ClarifyTaskRequest
	Clarifications        map[string][]TaskClarification
	RecoveryBlocks        map[string]RecoveryBlock
	HumanInputWaits       map[string]HumanInputWait
	HumanInputResponses   map[string]HumanInputResponse
	RetryDeadlines        map[string]time.Time
	ModelTurns            map[string]ModelTurnTelemetry
	IterationPlans        map[string]IterationPlan
	MaxParallel           int
	AllComplete           bool
}

// DispatchRequest is a commander request to start one ready task. RequestID is
// the idempotency boundary; ReadySetRevision prevents stale decisions.
type DispatchRequest struct {
	RequestID        string
	TaskName         string
	ReadySetRevision uint64
	Grant            ExecutionGrant
}

// ExecutionGrant is the immutable authorization and reproducibility envelope
// persisted on every accepted task attempt.
type ExecutionGrant struct {
	TaskName             string
	Model                string
	Tools                []string
	RunAs                string
	InputsJSON           string
	PolicyJSON           string
	ConfigurationVersion string
	Iteration            *IterationInput
	Dependencies         []DependencySnapshot
}

// DependencySnapshot is the accepted, model-visible output of a declared
// prerequisite at dispatch time. Raw provider output and worker transcripts
// never cross this boundary.
type DependencySnapshot struct {
	TaskName  string              `json:"taskName"`
	Status    ResultStatus        `json:"status"`
	Summary   string              `json:"summary"`
	Data      json.RawMessage     `json:"data,omitempty"`
	Artifacts []ArtifactReference `json:"artifacts,omitempty"`
}

// IterationInput is injected by the harness from the frozen plan, never from a
// commander-provided grant.
type IterationInput struct {
	ParentTask string          `json:"parentTask"`
	Index      int             `json:"index"`
	Item       json.RawMessage `json:"item"`
}

// IterationAggregate is an ordered, bounded projection of accepted child
// results. The full child envelopes remain addressable individually.
type IterationAggregate struct {
	Total        int                      `json:"total"`
	Items        []IterationAggregateItem `json:"items"`
	OmittedItems int                      `json:"omittedItems,omitempty"`
}

type IterationAggregateItem struct {
	Index          int                 `json:"index"`
	TaskName       string              `json:"taskName"`
	Summary        string              `json:"summary"`
	Data           json.RawMessage     `json:"data,omitempty"`
	Artifacts      []ArtifactReference `json:"artifacts,omitempty"`
	DetailsOmitted bool                `json:"detailsOmitted,omitempty"`
}

const maxIterationAggregateBytes = 1024 * 1024

// Dispatch is the accepted immutable identity for one task attempt.
type Dispatch struct {
	RunID          string
	RequestID      string
	AttemptID      string
	TaskName       string
	Attempt        int
	CommanderID    string
	StartRevision  uint64
	DispatchedAt   time.Time
	StartNotBefore time.Time
	Grant          ExecutionGrant
}

type ToolOperationStatus string

const (
	ToolOperationIntended  ToolOperationStatus = "intended"
	ToolOperationInFlight  ToolOperationStatus = "in_flight"
	ToolOperationCompleted ToolOperationStatus = "completed"
)

// ToolOperation records the durable boundary around one external invocation.
// An in-flight operation has an unknown outcome after a crash and must be
// reconciled rather than invoked again blindly.
type ToolOperation struct {
	ToolUseID string              `json:"toolUseId"`
	ToolName  string              `json:"toolName"`
	InputJSON string              `json:"inputJson"`
	Status    ToolOperationStatus `json:"status"`
	StartedAt time.Time           `json:"startedAt,omitempty"`
	Result    string              `json:"result,omitempty"`
	IsError   bool                `json:"isError,omitempty"`
}

// TaskAttemptProgress is the complete restart projection for a worker session.
// Failed attempts retain their transcript for operator inspection and retry
// history; completed attempts retain their transcript in AcceptedTaskResult.
type TaskAttemptProgress struct {
	AttemptID  string                   `json:"attemptId"`
	Phase      string                   `json:"phase"`
	Transcript []TaskSessionMessage     `json:"transcript,omitempty"`
	Tools      map[string]ToolOperation `json:"tools,omitempty"`
}

type RecoveryBlock struct {
	AttemptID      string `json:"attemptId"`
	TaskName       string `json:"taskName"`
	ToolUseID      string `json:"toolUseId"`
	ToolName       string `json:"toolName"`
	IdempotencyKey string `json:"idempotencyKey"`
	Reason         string `json:"reason"`
}

// HumanInputWait is a typed, durable pause for one in-flight tool operation.
// The request identity and absolute deadline survive worker replacement.
type HumanInputWait struct {
	AttemptID    string     `json:"attemptId"`
	TaskName     string     `json:"taskName"`
	ToolUseID    string     `json:"toolUseId"`
	ToolName     string     `json:"toolName"`
	ToolCallID   string     `json:"toolCallId"`
	Question     string     `json:"question"`
	ShortSummary string     `json:"shortSummary,omitempty"`
	Context      string     `json:"context,omitempty"`
	Choices      []string   `json:"choices,omitempty"`
	MultiSelect  bool       `json:"multiSelect,omitempty"`
	RequestedAt  time.Time  `json:"requestedAt"`
	DeadlineAt   *time.Time `json:"deadlineAt,omitempty"`
}

// HumanInputResponse is the accepted outcome, retained after the wait is
// removed so duplicate delivery cannot alter the original tool result.
type HumanInputResponse struct {
	AttemptID  string    `json:"attemptId"`
	ToolCallID string    `json:"toolCallId"`
	Response   string    `json:"response"`
	IsError    bool      `json:"isError,omitempty"`
	Responder  string    `json:"responder,omitempty"`
	ResolvedAt time.Time `json:"resolvedAt"`
}

// ExecutionLease is the persisted ownership fence for one active attempt.
// Token changes on every takeover, so a delayed worker from an older process
// cannot commit progress or a terminal transition to the same attempt.
type ExecutionLease struct {
	AttemptID string    `json:"attemptId"`
	OwnerID   string    `json:"ownerId"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ExecutionLeaseHeldError struct {
	AttemptID string
	OwnerID   string
	ExpiresAt time.Time
}

func (e *ExecutionLeaseHeldError) Error() string {
	return fmt.Sprintf("attempt %q is leased by %q until %s", e.AttemptID, e.OwnerID, e.ExpiresAt.UTC().Format(time.RFC3339Nano))
}

type ExecutionLeaseLostError struct {
	AttemptID string
}

func (e *ExecutionLeaseLostError) Error() string {
	return fmt.Sprintf("execution lease for attempt %q is no longer held", e.AttemptID)
}

// Checkpoint is the complete durable projection required to restore a harness
// without invoking a model, tool, or executor.
type Checkpoint struct {
	RunID                 string
	RunState              RunState
	Commander             CommanderSession
	Revision              uint64
	MaxParallel           int
	Definitions           []TaskDefinition
	TaskStates            map[string]TaskState
	Attempts              map[string]int
	Dispatches            map[string]Dispatch
	ActiveByTask          map[string]Dispatch
	TaskProgress          map[string]TaskAttemptProgress
	ExecutionLeases       map[string]ExecutionLease
	RecoveryBlocks        map[string]RecoveryBlock
	HumanInputWaits       map[string]HumanInputWait
	HumanInputResponses   map[string]HumanInputResponse
	RetryDeadlines        map[string]time.Time
	Results               map[string]AcceptedTaskResult
	Failures              map[string]TaskFailure
	Activations           map[string][]string
	RouteSelections       map[string]RouteSelection
	CrossMissionLaunches  map[string]CrossMissionLaunch
	PendingClarifications map[string]ClarifyTaskRequest
	Clarifications        map[string][]TaskClarification
	CommanderMessages     []CommanderMessage
	ModelTurns            map[string]ModelTurnTelemetry
	IterationPlans        map[string]IterationPlan
}

// Harness owns graph eligibility and state transitions for one mission run.
// The commander can request actions but cannot mutate these projections.
type Harness struct {
	mu sync.RWMutex

	runID       string
	runState    RunState
	commander   CommanderSession
	revision    uint64
	maxParallel int

	tasks                 map[string]TaskDefinition
	taskStates            map[string]TaskState
	attempts              map[string]int
	dispatches            map[string]Dispatch
	activeByTask          map[string]Dispatch
	taskProgress          map[string]TaskAttemptProgress
	executionLeases       map[string]ExecutionLease
	recoveryBlocks        map[string]RecoveryBlock
	humanInputWaits       map[string]HumanInputWait
	humanInputResponses   map[string]HumanInputResponse
	retryDeadlines        map[string]time.Time
	results               map[string]AcceptedTaskResult
	failures              map[string]TaskFailure
	activations           map[string][]string
	routeSelections       map[string]RouteSelection
	crossMissionLaunches  map[string]CrossMissionLaunch
	pendingClarifications map[string]ClarifyTaskRequest
	clarifications        map[string][]TaskClarification
	commanderMessages     []CommanderMessage
	modelTurns            map[string]ModelTurnTelemetry
	iterationPlans        map[string]IterationPlan
}

// NewHarness validates a mission graph and creates its pending run projection.
func NewHarness(runID, commanderSessionID string, definitions []TaskDefinition) (*Harness, error) {
	return NewHarnessWithMaxParallel(runID, commanderSessionID, definitions, 1)
}

func NewHarnessWithMaxParallel(runID, commanderSessionID string, definitions []TaskDefinition, maxParallel int) (*Harness, error) {
	if runID == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	if commanderSessionID == "" {
		return nil, fmt.Errorf("commander session ID is required")
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("at least one task is required")
	}
	if maxParallel <= 0 {
		maxParallel = 1
	}

	tasks := make(map[string]TaskDefinition, len(definitions))
	statesByTask := make(map[string]TaskState, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			return nil, fmt.Errorf("task name is required")
		}
		if _, exists := tasks[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate task %q", definition.Name)
		}
		definition.DependsOn = append([]string(nil), definition.DependsOn...)
		definition.SendTo = append([]string(nil), definition.SendTo...)
		definition.Routes = append([]RouteDefinition(nil), definition.Routes...)
		if definition.MaxRetries < 0 {
			return nil, fmt.Errorf("task %q max retries cannot be negative", definition.Name)
		}
		if err := validateRetryDelay(definition); err != nil {
			return nil, err
		}
		definition.ResultPolicy = cloneResultPolicy(definition.ResultPolicy)
		if definition.Iterator != nil {
			policy := *definition.Iterator
			definition.Iterator = &policy
		}
		tasks[definition.Name] = definition
		statesByTask[definition.Name] = TaskBlocked
	}
	for _, definition := range tasks {
		for _, dependency := range definition.DependsOn {
			if _, exists := tasks[dependency]; !exists {
				return nil, fmt.Errorf("task %q depends on unknown task %q", definition.Name, dependency)
			}
		}
		for _, target := range definition.SendTo {
			if _, exists := tasks[target]; !exists {
				return nil, fmt.Errorf("task %q sends to unknown task %q", definition.Name, target)
			}
		}
		for _, route := range definition.Routes {
			if route.Target == "" || route.Condition == "" {
				return nil, fmt.Errorf("task %q has an invalid route", definition.Name)
			}
			if !route.IsMission {
				if _, exists := tasks[route.Target]; !exists {
					return nil, fmt.Errorf("task %q routes to unknown task %q", definition.Name, route.Target)
				}
			}
		}
	}
	dynamicTargets := make(map[string]bool)
	for _, definition := range tasks {
		for _, target := range definition.SendTo {
			dynamicTargets[target] = true
		}
		for _, route := range definition.Routes {
			if !route.IsMission {
				dynamicTargets[route.Target] = true
			}
		}
	}
	for target := range dynamicTargets {
		if len(tasks[target].DependsOn) > 0 {
			return nil, fmt.Errorf("dynamically activated task %q cannot have dependencies", target)
		}
	}
	if err := validateAcyclic(tasks); err != nil {
		return nil, err
	}

	return &Harness{
		runID:                 runID,
		runState:              RunPending,
		commander:             CommanderSession{ID: commanderSessionID, State: CommanderInitializing},
		maxParallel:           maxParallel,
		tasks:                 tasks,
		taskStates:            statesByTask,
		attempts:              make(map[string]int, len(tasks)),
		dispatches:            make(map[string]Dispatch),
		activeByTask:          make(map[string]Dispatch),
		taskProgress:          make(map[string]TaskAttemptProgress),
		executionLeases:       make(map[string]ExecutionLease),
		recoveryBlocks:        make(map[string]RecoveryBlock),
		humanInputWaits:       make(map[string]HumanInputWait),
		humanInputResponses:   make(map[string]HumanInputResponse),
		retryDeadlines:        make(map[string]time.Time),
		results:               make(map[string]AcceptedTaskResult),
		failures:              make(map[string]TaskFailure),
		activations:           make(map[string][]string),
		routeSelections:       make(map[string]RouteSelection),
		crossMissionLaunches:  make(map[string]CrossMissionLaunch),
		pendingClarifications: make(map[string]ClarifyTaskRequest),
		clarifications:        make(map[string][]TaskClarification),
		modelTurns:            make(map[string]ModelTurnTelemetry),
		iterationPlans:        make(map[string]IterationPlan),
	}, nil
}

// Start activates the run and its existing commander session, then publishes
// the first ready-set revision.
func (h *Harness) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !CanTransitionRun(h.runState, RunRunning) {
		return fmt.Errorf("cannot start run from %q", h.runState)
	}
	if !CanTransitionCommander(h.commander.State, CommanderActive) {
		return fmt.Errorf("cannot activate commander from %q", h.commander.State)
	}
	h.runState = RunRunning
	h.commander.State = CommanderActive
	h.publishReadySetLocked()
	return nil
}

// ExpandIteration freezes a ready parent's dataset into durable child task
// instances. The parent itself is never dispatched to a worker.
func (h *Harness) ExpandIteration(plan IterationPlan) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runState != RunRunning || h.commander.State != CommanderActive {
		return fmt.Errorf("run is not accepting iteration expansion")
	}
	parent, exists := h.tasks[plan.ParentTask]
	if !exists || parent.Iterator == nil {
		return fmt.Errorf("task %q is not an iterated task", plan.ParentTask)
	}
	if previous, exists := h.iterationPlans[plan.ParentTask]; exists {
		if reflect.DeepEqual(previous, plan) {
			return nil
		}
		return fmt.Errorf("iteration %q already has a different frozen plan", plan.ParentTask)
	}
	if h.taskStates[plan.ParentTask] != TaskReady {
		return fmt.Errorf("iteration %q is not ready", plan.ParentTask)
	}
	items := make([]json.RawMessage, len(plan.Children))
	for index, child := range plan.Children {
		items[index] = child.Item
	}
	canonical, err := BuildIterationPlan(plan.ParentTask, *parent.Iterator, items)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(canonical, plan) {
		return fmt.Errorf("iteration %q plan is not canonical for its declared policy", plan.ParentTask)
	}
	for _, child := range plan.Children {
		if _, exists := h.tasks[child.Name]; exists {
			return fmt.Errorf("iteration child %q collides with a declared task", child.Name)
		}
	}
	h.iterationPlans[plan.ParentTask] = cloneIterationPlan(plan)
	h.taskStates[plan.ParentTask] = TaskWaiting
	for _, child := range plan.Children {
		h.tasks[child.Name] = TaskDefinition{
			Name: child.Name, Objective: parent.Objective,
			DependsOn:         append([]string(nil), child.DependsOn...),
			MaxRetries:        plan.Policy.MaxRetries,
			RetryDelaySeconds: parent.RetryDelaySeconds,
			ResultPolicy:      cloneResultPolicy(parent.ResultPolicy),
			ParentTask:        plan.ParentTask,
		}
		h.taskStates[child.Name] = TaskBlocked
	}
	if len(plan.Children) == 0 {
		h.completeIterationParentLocked(plan.ParentTask)
	}
	h.publishReadySetLocked()
	if h.allCompleteLocked() {
		h.runState = RunCompleted
		h.commander.State = CommanderCompleted
		h.revision++
	}
	return nil
}

func (h *Harness) completeIterationParentLocked(parentName string) {
	plan := h.iterationPlans[parentName]
	for _, child := range plan.Children {
		if h.taskStates[child.Name] != TaskCompleted {
			return
		}
	}
	if h.taskStates[parentName] == TaskCompleted {
		return
	}
	summary := fmt.Sprintf("Completed %d iteration items", len(plan.Children))
	aggregate := h.iterationAggregateLocked(plan)
	aggregateJSON, _ := json.Marshal(aggregate) // accepted child data is already valid JSON
	h.results[parentName] = AcceptedTaskResult{
		Envelope: TaskResultEnvelope{
			Status: ResultSucceeded, Summary: summary,
			Content: []store.MessagePart{{Type: "text", Text: summary}}, Data: aggregateJSON,
			Provenance: ResultProvenance{RunID: h.runID, TaskName: parentName, AttemptID: "iteration:" + plan.DatasetDigest, Model: "harness"},
		},
		Validation: ResultValidation{Valid: true},
	}
	h.taskStates[parentName] = TaskCompleted
	for _, target := range h.tasks[parentName].SendTo {
		h.activateLocked(target, parentName)
	}
}

func (h *Harness) iterationAggregateLocked(plan IterationPlan) IterationAggregate {
	aggregate := IterationAggregate{Total: len(plan.Children), Items: make([]IterationAggregateItem, 0, len(plan.Children))}
	used := 64 // reserve space for the enclosing object and omitted count
	for _, child := range plan.Children {
		envelope := h.results[child.Name].Envelope
		entry := IterationAggregateItem{
			Index: child.Index, TaskName: child.Name, Summary: envelope.Summary,
			Data:      append(json.RawMessage(nil), envelope.Data...),
			Artifacts: append([]ArtifactReference(nil), envelope.Artifacts...),
		}
		encoded, _ := json.Marshal(entry)
		if used+len(encoded)+1 > maxIterationAggregateBytes {
			entry.Data, entry.Artifacts, entry.DetailsOmitted = nil, nil, true
			if runes := []rune(entry.Summary); len(runes) > 512 {
				entry.Summary = string(runes[:512])
			}
			encoded, _ = json.Marshal(entry)
		}
		if used+len(encoded)+1 > maxIterationAggregateBytes {
			break
		}
		aggregate.Items = append(aggregate.Items, entry)
		used += len(encoded) + 1
	}
	aggregate.OmittedItems = aggregate.Total - len(aggregate.Items)
	return aggregate
}

// Stop pauses a quiescent run and its existing commander session. Active task
// attempts must reach a terminal or waiting boundary before a graceful stop.
func (h *Harness) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if (h.runState != RunRunning && h.runState != RunWaiting) ||
		(h.commander.State != CommanderActive && h.commander.State != CommanderWaiting) {
		return fmt.Errorf("run is not active")
	}
	for taskName := range h.activeByTask {
		if h.taskStates[taskName] == TaskRunning || h.taskStates[taskName] == TaskDispatching {
			return fmt.Errorf("cannot stop run with executable task attempts")
		}
	}
	if !CanTransitionRun(h.runState, RunStopping) || !CanTransitionRun(RunStopping, RunStopped) {
		return fmt.Errorf("run cannot transition to stopped")
	}
	if !CanTransitionCommander(h.commander.State, CommanderStopping) || !CanTransitionCommander(CommanderStopping, CommanderStopped) {
		return fmt.Errorf("commander cannot transition to stopped")
	}
	h.runState = RunStopped
	h.commander.State = CommanderStopped
	h.revision++
	return nil
}

// FailRun records an execution failure that did not belong to a task attempt,
// such as a commander or provider error. Task-attempt failures use FailTask so
// their retry policy remains authoritative.
func (h *Harness) FailRun() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !CanTransitionRun(h.runState, RunFailed) || !CanTransitionCommander(h.commander.State, CommanderFailed) {
		return fmt.Errorf("run cannot fail from %q with commander %q", h.runState, h.commander.State)
	}
	h.runState = RunFailed
	h.commander.State = CommanderFailed
	h.revision++
	return nil
}

// Resume reactivates the same stopped run and commander session.
func (h *Harness) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !CanTransitionRun(h.runState, RunRunning) {
		return fmt.Errorf("cannot resume run from %q", h.runState)
	}
	if !CanTransitionCommander(h.commander.State, CommanderActive) {
		return fmt.Errorf("cannot resume commander from %q", h.commander.State)
	}
	h.runState = RunRunning
	h.commander.State = CommanderActive
	h.publishReadySetLocked()
	return nil
}

// Snapshot returns a defensive copy of the current commander projection.
func (h *Harness) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked()
}

// Checkpoint returns a defensive copy of the complete durable projection.
func (h *Harness) Checkpoint() Checkpoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.checkpointLocked()
}

// CommanderMessages returns a defensive copy of the run-scoped commander
// conversation.
func (h *Harness) CommanderMessages() []CommanderMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneCommanderMessages(h.commanderMessages)
}

// TaskResult returns the full validated result for explicit inspection.
func (h *Harness) TaskResult(taskName string) (AcceptedTaskResult, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result, ok := h.results[taskName]
	if !ok {
		return AcceptedTaskResult{}, false
	}
	return cloneAcceptedResults(map[string]AcceptedTaskResult{taskName: result})[taskName], true
}

func (h *Harness) TaskProgress(attemptID string) (TaskAttemptProgress, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	progress, ok := h.taskProgress[attemptID]
	return cloneTaskProgress(progress), ok
}

func (h *Harness) AcquireExecutionLease(attemptID, ownerID, token string, now time.Time, ttl time.Duration) (ExecutionLease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ownerID == "" || token == "" || ttl <= 0 {
		return ExecutionLease{}, fmt.Errorf("execution lease requires owner, token, and positive TTL")
	}
	if !h.attemptActiveLocked(attemptID) {
		return ExecutionLease{}, fmt.Errorf("attempt %q is not active", attemptID)
	}
	if current, ok := h.executionLeases[attemptID]; ok && current.ExpiresAt.After(now) {
		if current.OwnerID == ownerID && current.Token == token {
			return current, nil
		}
		return ExecutionLease{}, &ExecutionLeaseHeldError{AttemptID: attemptID, OwnerID: current.OwnerID, ExpiresAt: current.ExpiresAt}
	}
	lease := ExecutionLease{AttemptID: attemptID, OwnerID: ownerID, Token: token, ExpiresAt: now.Add(ttl).UTC()}
	h.executionLeases[attemptID] = lease
	h.revision++
	return lease, nil
}

func (h *Harness) RenewExecutionLease(attemptID, token string, now time.Time, ttl time.Duration) (ExecutionLease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	lease, ok := h.executionLeases[attemptID]
	if !ok || lease.Token != token || !lease.ExpiresAt.After(now) || !h.attemptActiveLocked(attemptID) {
		return ExecutionLease{}, &ExecutionLeaseLostError{AttemptID: attemptID}
	}
	if ttl <= 0 {
		return ExecutionLease{}, fmt.Errorf("execution lease TTL must be positive")
	}
	lease.ExpiresAt = now.Add(ttl).UTC()
	h.executionLeases[attemptID] = lease
	h.revision++
	return lease, nil
}

func (h *Harness) ReleaseExecutionLease(attemptID, token string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(attemptID, token); err != nil {
		return err
	}
	delete(h.executionLeases, attemptID)
	h.revision++
	return nil
}

func (h *Harness) attemptActiveLocked(attemptID string) bool {
	for _, dispatch := range h.activeByTask {
		if dispatch.AttemptID == attemptID {
			return true
		}
	}
	return false
}

func (h *Harness) requireExecutionLeaseLocked(attemptID, token string) error {
	lease, exists := h.executionLeases[attemptID]
	if !exists {
		if token == "" {
			return nil
		}
		return &ExecutionLeaseLostError{AttemptID: attemptID}
	}
	if token == "" || lease.Token != token || !lease.ExpiresAt.After(time.Now().UTC()) {
		return &ExecutionLeaseLostError{AttemptID: attemptID}
	}
	return nil
}

func (h *Harness) SaveTaskProgress(dispatch Dispatch, progress TaskAttemptProgress) error {
	return h.SaveTaskProgressWithLease(dispatch, progress, "")
}

func (h *Harness) SaveTaskProgressWithLease(dispatch Dispatch, progress TaskAttemptProgress, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(dispatch.AttemptID, leaseToken); err != nil {
		return err
	}
	active, ok := h.activeByTask[dispatch.TaskName]
	if !ok || active.AttemptID != dispatch.AttemptID || progress.AttemptID != dispatch.AttemptID {
		return fmt.Errorf("attempt %q is not active for task %q", dispatch.AttemptID, dispatch.TaskName)
	}
	if progress.Phase == "" {
		return fmt.Errorf("task progress phase is required")
	}
	previous := h.taskProgress[dispatch.AttemptID]
	if reflect.DeepEqual(previous, progress) {
		return nil
	}
	if len(progress.Transcript) < len(previous.Transcript) {
		return fmt.Errorf("task transcript cannot move backwards")
	}
	for id, operation := range progress.Tools {
		if id == "" || operation.ToolUseID != id || operation.ToolName == "" {
			return fmt.Errorf("task progress contains an invalid tool operation")
		}
		prior, exists := previous.Tools[id]
		if !exists {
			continue
		}
		if prior.ToolName != operation.ToolName || prior.InputJSON != operation.InputJSON {
			return fmt.Errorf("tool operation %q identity changed", id)
		}
		if !prior.StartedAt.IsZero() && !prior.StartedAt.Equal(operation.StartedAt) {
			return fmt.Errorf("tool operation %q start time changed", id)
		}
		if toolStatusRank(operation.Status) < toolStatusRank(prior.Status) {
			return fmt.Errorf("tool operation %q moved backwards", id)
		}
		if prior.Status == ToolOperationCompleted && !reflect.DeepEqual(prior, operation) {
			return fmt.Errorf("completed tool operation %q is immutable", id)
		}
	}
	if err := h.recordTaskTranscriptTurnsLocked(progress.Transcript); err != nil {
		return err
	}
	h.taskProgress[dispatch.AttemptID] = cloneTaskProgress(progress)
	h.revision++
	return nil
}

func (h *Harness) BlockTaskRecovery(dispatch Dispatch, block RecoveryBlock) error {
	return h.BlockTaskRecoveryWithLease(dispatch, block, "")
}

func (h *Harness) BlockTaskRecoveryWithLease(dispatch Dispatch, block RecoveryBlock, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(dispatch.AttemptID, leaseToken); err != nil {
		return err
	}
	active, ok := h.activeByTask[dispatch.TaskName]
	if !ok || active.AttemptID != dispatch.AttemptID || block.AttemptID != dispatch.AttemptID || block.TaskName != dispatch.TaskName {
		return fmt.Errorf("attempt %q is not active for task %q", dispatch.AttemptID, dispatch.TaskName)
	}
	if block.ToolUseID == "" || block.ToolName == "" || block.IdempotencyKey == "" || block.Reason == "" {
		return fmt.Errorf("recovery block requires tool identity, idempotency key, and reason")
	}
	progress := h.taskProgress[dispatch.AttemptID]
	operation, ok := progress.Tools[block.ToolUseID]
	if !ok || operation.Status != ToolOperationInFlight || operation.ToolName != block.ToolName {
		return fmt.Errorf("tool operation %q is not in flight", block.ToolUseID)
	}
	if previous, exists := h.recoveryBlocks[dispatch.AttemptID]; exists {
		if reflect.DeepEqual(previous, block) {
			return nil
		}
		return fmt.Errorf("attempt %q already has a different recovery block", dispatch.AttemptID)
	}
	if h.taskStates[dispatch.TaskName] != TaskRunning || !CanTransitionTask(TaskRunning, TaskWaiting) {
		return fmt.Errorf("task %q cannot wait for recovery from %q", dispatch.TaskName, h.taskStates[dispatch.TaskName])
	}
	h.taskStates[dispatch.TaskName] = TaskWaiting
	h.recoveryBlocks[dispatch.AttemptID] = block
	delete(h.executionLeases, dispatch.AttemptID)
	h.revision++
	return nil
}

// WaitTaskForHumanInputWithLease parks one logical attempt and releases its
// execution lease. The in-flight tool intent must already be checkpointed.
func (h *Harness) WaitTaskForHumanInputWithLease(dispatch Dispatch, wait HumanInputWait, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(dispatch.AttemptID, leaseToken); err != nil {
		return err
	}
	active, ok := h.activeByTask[dispatch.TaskName]
	if !ok || active.AttemptID != dispatch.AttemptID || wait.AttemptID != dispatch.AttemptID || wait.TaskName != dispatch.TaskName {
		return fmt.Errorf("attempt %q is not active for task %q", dispatch.AttemptID, dispatch.TaskName)
	}
	if wait.ToolUseID == "" || wait.ToolName == "" || wait.ToolCallID == "" || wait.Question == "" || wait.RequestedAt.IsZero() {
		return fmt.Errorf("human input wait requires tool identity, question, and request time")
	}
	if wait.DeadlineAt != nil && !wait.DeadlineAt.After(wait.RequestedAt) {
		return fmt.Errorf("human input deadline must follow request time")
	}
	progress := h.taskProgress[dispatch.AttemptID]
	operation, ok := progress.Tools[wait.ToolUseID]
	if !ok || operation.Status != ToolOperationInFlight || operation.ToolName != wait.ToolName {
		return fmt.Errorf("human input tool operation %q is not in flight", wait.ToolUseID)
	}
	if _, exists := h.recoveryBlocks[dispatch.AttemptID]; exists {
		return fmt.Errorf("attempt %q is already blocked for recovery", dispatch.AttemptID)
	}
	if previous, exists := h.humanInputWaits[dispatch.AttemptID]; exists {
		if reflect.DeepEqual(previous, wait) {
			return nil
		}
		return fmt.Errorf("attempt %q already has a different human input wait", dispatch.AttemptID)
	}
	if h.taskStates[dispatch.TaskName] != TaskRunning || !CanTransitionTask(TaskRunning, TaskWaiting) {
		return fmt.Errorf("task %q cannot wait for human input from %q", dispatch.TaskName, h.taskStates[dispatch.TaskName])
	}
	h.taskStates[dispatch.TaskName] = TaskWaiting
	h.humanInputWaits[dispatch.AttemptID] = cloneHumanInputWait(wait)
	delete(h.executionLeases, dispatch.AttemptID)
	h.revision++
	return nil
}

// ResolveHumanInputWait accepts the first response and resumes the original
// attempt; redelivery is idempotent, while a different response is rejected.
func (h *Harness) ResolveHumanInputWait(attemptID, toolCallID, response, responder string, isError bool, resolvedAt time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if previous, ok := h.humanInputResponses[toolCallID]; ok {
		if previous.AttemptID == attemptID && previous.Response == response && previous.IsError == isError && previous.Responder == responder {
			return nil
		}
		return fmt.Errorf("human input %q already has a different response", toolCallID)
	}
	wait, ok := h.humanInputWaits[attemptID]
	if !ok || wait.ToolCallID != toolCallID {
		return fmt.Errorf("attempt %q has no human input wait %q", attemptID, toolCallID)
	}
	if resolvedAt.IsZero() {
		return fmt.Errorf("human input resolution time is required")
	}
	if wait.DeadlineAt != nil && !resolvedAt.Before(*wait.DeadlineAt) && !isError {
		return fmt.Errorf("human input %q was answered after its deadline", toolCallID)
	}
	progress := h.taskProgress[attemptID]
	operation, ok := progress.Tools[wait.ToolUseID]
	if !ok || operation.Status != ToolOperationInFlight {
		return fmt.Errorf("human input tool operation %q is not in flight", wait.ToolUseID)
	}
	if h.taskStates[wait.TaskName] != TaskWaiting || !CanTransitionTask(TaskWaiting, TaskRunning) {
		return fmt.Errorf("task %q cannot resume from %q", wait.TaskName, h.taskStates[wait.TaskName])
	}
	operation.Status, operation.Result, operation.IsError = ToolOperationCompleted, response, isError
	progress.Tools[wait.ToolUseID] = operation
	h.taskProgress[attemptID] = progress
	h.humanInputResponses[toolCallID] = HumanInputResponse{
		AttemptID: attemptID, ToolCallID: toolCallID, Response: response, IsError: isError,
		Responder: responder, ResolvedAt: resolvedAt,
	}
	delete(h.humanInputWaits, attemptID)
	h.taskStates[wait.TaskName] = TaskRunning
	if h.runState == RunWaiting {
		h.runState = RunRunning
		h.commander.State = CommanderActive
	}
	h.revision++
	return nil
}

func (h *Harness) EnterRecoveryWaiting() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.recoveryBlocks) == 0 && len(h.humanInputWaits) == 0 && len(h.retryDeadlines) == 0 {
		return fmt.Errorf("run has no waiting operations")
	}
	for _, state := range h.taskStates {
		if state == TaskRunning || state == TaskDispatching {
			return fmt.Errorf("run still has executable task attempts")
		}
	}
	if h.runState == RunWaiting && h.commander.State == CommanderWaiting {
		return nil
	}
	if !CanTransitionRun(h.runState, RunWaiting) || !CanTransitionCommander(h.commander.State, CommanderWaiting) {
		return fmt.Errorf("run cannot enter recovery waiting from %q", h.runState)
	}
	h.runState = RunWaiting
	h.commander.State = CommanderWaiting
	h.revision++
	return nil
}

func (h *Harness) ResolveRecoveryBlock(attemptID, result string, isError bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	block, ok := h.recoveryBlocks[attemptID]
	if !ok {
		return fmt.Errorf("attempt %q has no recovery block", attemptID)
	}
	progress := h.taskProgress[attemptID]
	operation, ok := progress.Tools[block.ToolUseID]
	if !ok || operation.Status != ToolOperationInFlight {
		return fmt.Errorf("blocked tool operation %q is not in flight", block.ToolUseID)
	}
	operation.Status = ToolOperationCompleted
	operation.Result = result
	operation.IsError = isError
	progress.Tools[block.ToolUseID] = operation
	h.taskProgress[attemptID] = progress
	if h.taskStates[block.TaskName] != TaskWaiting || !CanTransitionTask(TaskWaiting, TaskRunning) {
		return fmt.Errorf("task %q cannot resume from %q", block.TaskName, h.taskStates[block.TaskName])
	}
	h.taskStates[block.TaskName] = TaskRunning
	delete(h.recoveryBlocks, attemptID)
	if h.runState == RunWaiting {
		if !CanTransitionRun(RunWaiting, RunRunning) || !CanTransitionCommander(CommanderWaiting, CommanderActive) {
			return fmt.Errorf("run cannot resume after reconciliation")
		}
		h.runState = RunRunning
		h.commander.State = CommanderActive
	}
	h.revision++
	return nil
}

func toolStatusRank(status ToolOperationStatus) int {
	switch status {
	case ToolOperationIntended:
		return 1
	case ToolOperationInFlight:
		return 2
	case ToolOperationCompleted:
		return 3
	default:
		return 0
	}
}

func (h *Harness) InspectTask(request InspectTaskRequest) (TaskInspection, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if request.RequestID == "" {
		return TaskInspection{}, fmt.Errorf("request ID is required")
	}
	if request.Revision != h.revision {
		return TaskInspection{}, fmt.Errorf("stale inspection revision: got %d, current %d", request.Revision, h.revision)
	}
	accepted, ok := h.results[request.TaskName]
	if !ok || h.taskStates[request.TaskName] != TaskCompleted {
		return TaskInspection{}, fmt.Errorf("task %q has no accepted result", request.TaskName)
	}
	envelope := cloneTaskResultEnvelope(accepted.Envelope)
	return TaskInspection{
		RequestID: request.RequestID, TaskName: request.TaskName, Status: envelope.Status, Summary: envelope.Summary,
		Content: envelope.Content, Artifacts: envelope.Artifacts, Data: append([]byte(nil), envelope.Data...),
		Diagnostics: append([]byte(nil), envelope.Diagnostics...), Provenance: envelope.Provenance,
	}, nil
}

// BeginClarification durably fences a provider call. A restored coordinator
// can resume any pending request without asking the commander again.
func (h *Harness) BeginClarification(request ClarifyTaskRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.RequestID == "" || request.MessageID == "" || request.TaskName == "" || strings.TrimSpace(request.Question) == "" {
		return fmt.Errorf("clarification request ID, message ID, task name, and question are required")
	}
	if utf8.RuneCountInString(request.Question) > 4096 {
		return fmt.Errorf("clarification question exceeds 4096 characters")
	}
	if previous, ok := h.pendingClarifications[request.RequestID]; ok {
		if reflect.DeepEqual(previous, request) {
			return nil
		}
		return fmt.Errorf("clarification request %q already exists", request.RequestID)
	}
	for _, completed := range h.clarifications[request.TaskName] {
		if completed.RequestID == request.RequestID {
			return nil
		}
	}
	if request.Revision != h.revision {
		return fmt.Errorf("stale clarification revision: got %d, current %d", request.Revision, h.revision)
	}
	if h.runState != RunRunning || h.commander.State != CommanderActive {
		return fmt.Errorf("run is not accepting task clarifications")
	}
	definition, declared := h.tasks[request.TaskName]
	if !declared || len(definition.Routes) == 0 {
		return fmt.Errorf("task %q does not have a pending route", request.TaskName)
	}
	if _, selected := h.routeSelections[request.TaskName]; selected {
		return fmt.Errorf("task %q does not have a pending route", request.TaskName)
	}
	accepted, ok := h.results[request.TaskName]
	if !ok || h.taskStates[request.TaskName] != TaskCompleted {
		return fmt.Errorf("task %q has no accepted result", request.TaskName)
	}
	if len(accepted.SessionTranscript) == 0 {
		return fmt.Errorf("task %q has no restorable session transcript", request.TaskName)
	}
	if len(h.clarifications[request.TaskName]) >= maxTaskClarifications {
		return fmt.Errorf("task %q reached its clarification limit", request.TaskName)
	}
	for _, pending := range h.pendingClarifications {
		if pending.TaskName == request.TaskName {
			return fmt.Errorf("task %q already has a pending clarification", request.TaskName)
		}
	}
	h.pendingClarifications[request.RequestID] = request
	h.revision++
	return nil
}

func (h *Harness) ClarificationContext(requestID string) (ClarifyTaskRequest, Dispatch, []TaskSessionMessage, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	request, ok := h.pendingClarifications[requestID]
	if !ok {
		return ClarifyTaskRequest{}, Dispatch{}, nil, fmt.Errorf("clarification request %q is not pending", requestID)
	}
	accepted := h.results[request.TaskName]
	var dispatch Dispatch
	for _, candidate := range h.dispatches {
		if candidate.AttemptID == accepted.Envelope.Provenance.AttemptID {
			dispatch = candidate
			break
		}
	}
	if dispatch.AttemptID == "" {
		return ClarifyTaskRequest{}, Dispatch{}, nil, fmt.Errorf("accepted dispatch for task %q was not found", request.TaskName)
	}
	return request, cloneDispatch(dispatch), cloneTaskSessionMessages(accepted.SessionTranscript), nil
}

func (h *Harness) CompleteClarification(requestID string, result TaskClarificationResult) (TaskClarification, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	request, ok := h.pendingClarifications[requestID]
	if !ok {
		for _, items := range h.clarifications {
			for _, item := range items {
				if item.RequestID == requestID {
					return cloneTaskClarification(item), nil
				}
			}
		}
		return TaskClarification{}, fmt.Errorf("clarification request %q is not pending", requestID)
	}
	if len(result.Answer) == 0 || len(result.SessionTranscript) == 0 {
		return TaskClarification{}, fmt.Errorf("clarification answer and updated session transcript are required")
	}
	if err := h.recordTaskTranscriptTurnsLocked(result.SessionTranscript); err != nil {
		return TaskClarification{}, err
	}
	accepted := h.results[request.TaskName]
	accepted.SessionTranscript = cloneTaskSessionMessages(result.SessionTranscript)
	h.results[request.TaskName] = accepted
	clarification := TaskClarification{RequestID: request.RequestID, TaskName: request.TaskName, Question: request.Question, Answer: append([]store.MessagePart(nil), result.Answer...)}
	h.clarifications[request.TaskName] = append(h.clarifications[request.TaskName], clarification)
	delete(h.pendingClarifications, requestID)
	h.revision++
	return cloneTaskClarification(clarification), nil
}

// AppendCommanderMessage appends one immutable structured message. Repeating
// the same message ID with identical content is idempotent; conflicting content
// is rejected.
func (h *Harness) AppendCommanderMessage(message CommanderMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if message.ID == "" {
		return fmt.Errorf("commander message ID is required")
	}
	if message.Role == "" {
		return fmt.Errorf("commander message role is required")
	}
	if len(message.Parts) == 0 {
		return fmt.Errorf("commander message parts are required")
	}
	for _, existing := range h.commanderMessages {
		if existing.ID != message.ID {
			continue
		}
		if reflect.DeepEqual(existing, message) {
			return nil
		}
		return fmt.Errorf("commander message %q already exists with different content", message.ID)
	}
	if h.commander.State != CommanderActive && h.commander.State != CommanderWaiting {
		return fmt.Errorf("commander session is not accepting messages")
	}
	if message.Telemetry != nil {
		if err := h.recordModelTurnLocked(*message.Telemetry); err != nil {
			return err
		}
	}
	message.Parts = append([]store.MessagePart(nil), message.Parts...)
	h.commanderMessages = append(h.commanderMessages, message)
	h.revision++
	return nil
}

// RestoreHarness reconstructs a harness from a previously committed
// checkpoint. Restoration performs no external work.
func RestoreHarness(checkpoint Checkpoint) (*Harness, error) {
	if checkpoint.RunID == "" {
		return nil, fmt.Errorf("checkpoint run ID is required")
	}
	if checkpoint.Commander.ID == "" {
		return nil, fmt.Errorf("checkpoint commander session ID is required")
	}
	if len(checkpoint.Definitions) == 0 {
		return nil, fmt.Errorf("checkpoint task definitions are required")
	}
	if checkpoint.MaxParallel <= 0 {
		checkpoint.MaxParallel = 1
	}

	definitions := make(map[string]TaskDefinition, len(checkpoint.Definitions))
	for _, definition := range checkpoint.Definitions {
		if _, exists := definitions[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate checkpoint task %q", definition.Name)
		}
		definition.DependsOn = append([]string(nil), definition.DependsOn...)
		definition.SendTo = append([]string(nil), definition.SendTo...)
		definition.Routes = append([]RouteDefinition(nil), definition.Routes...)
		definition.ResultPolicy = cloneResultPolicy(definition.ResultPolicy)
		if err := validateRetryDelay(definition); err != nil {
			return nil, err
		}
		if definition.Iterator != nil {
			policy := *definition.Iterator
			definition.Iterator = &policy
		}
		definitions[definition.Name] = definition
	}
	if err := validateAcyclic(definitions); err != nil {
		return nil, err
	}

	taskStates := cloneMap(checkpoint.TaskStates)
	for name := range definitions {
		if _, ok := taskStates[name]; !ok {
			return nil, fmt.Errorf("checkpoint is missing state for task %q", name)
		}
	}
	for name := range taskStates {
		if _, ok := definitions[name]; !ok {
			return nil, fmt.Errorf("checkpoint contains unknown task state %q", name)
		}
	}

	return &Harness{
		runID:                 checkpoint.RunID,
		runState:              checkpoint.RunState,
		commander:             checkpoint.Commander,
		revision:              checkpoint.Revision,
		maxParallel:           checkpoint.MaxParallel,
		tasks:                 definitions,
		taskStates:            taskStates,
		attempts:              cloneMap(checkpoint.Attempts),
		dispatches:            cloneDispatchMap(checkpoint.Dispatches),
		activeByTask:          cloneDispatchMap(checkpoint.ActiveByTask),
		taskProgress:          cloneTaskProgressMap(checkpoint.TaskProgress),
		executionLeases:       cloneMap(checkpoint.ExecutionLeases),
		recoveryBlocks:        cloneMap(checkpoint.RecoveryBlocks),
		humanInputWaits:       cloneHumanInputWaits(checkpoint.HumanInputWaits),
		humanInputResponses:   cloneMap(checkpoint.HumanInputResponses),
		retryDeadlines:        cloneMap(checkpoint.RetryDeadlines),
		results:               cloneAcceptedResults(checkpoint.Results),
		failures:              cloneMap(checkpoint.Failures),
		activations:           cloneStringSliceMap(checkpoint.Activations),
		routeSelections:       cloneRouteSelections(checkpoint.RouteSelections),
		crossMissionLaunches:  cloneCrossMissionLaunches(checkpoint.CrossMissionLaunches),
		pendingClarifications: cloneMap(checkpoint.PendingClarifications),
		clarifications:        cloneClarifications(checkpoint.Clarifications),
		commanderMessages:     cloneCommanderMessages(checkpoint.CommanderMessages),
		modelTurns:            cloneMap(checkpoint.ModelTurns),
		iterationPlans:        cloneIterationPlans(checkpoint.IterationPlans),
	}, nil
}

// DispatchTask validates and applies one commander dispatch request.
func (h *Harness) DispatchTask(request DispatchRequest) (Dispatch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if request.RequestID == "" {
		return Dispatch{}, fmt.Errorf("request ID is required")
	}
	if previous, ok := h.dispatches[request.RequestID]; ok {
		if previous.TaskName != request.TaskName {
			return Dispatch{}, fmt.Errorf("request %q was already used for task %q", request.RequestID, previous.TaskName)
		}
		return cloneDispatch(previous), nil
	}
	if h.runState != RunRunning || h.commander.State != CommanderActive {
		return Dispatch{}, fmt.Errorf("run is not accepting dispatches")
	}
	if request.ReadySetRevision != h.revision {
		return Dispatch{}, fmt.Errorf("stale ready-set revision: got %d, current %d", request.ReadySetRevision, h.revision)
	}
	state, exists := h.taskStates[request.TaskName]
	if !exists {
		return Dispatch{}, fmt.Errorf("task %q is not declared", request.TaskName)
	}
	if state != TaskReady {
		return Dispatch{}, fmt.Errorf("task %q is not ready", request.TaskName)
	}
	if h.executableAttemptCountLocked() >= h.maxParallel {
		return Dispatch{}, fmt.Errorf("run has reached its maximum of %d parallel tasks", h.maxParallel)
	}
	definition := h.tasks[request.TaskName]
	if definition.Iterator != nil {
		return Dispatch{}, fmt.Errorf("iterated task %q must expand before dispatch", request.TaskName)
	}
	if definition.ParentTask != "" {
		plan := h.iterationPlans[definition.ParentTask]
		activeChildren := 0
		for name := range h.activeByTask {
			if h.tasks[name].ParentTask == definition.ParentTask {
				activeChildren++
			}
		}
		if activeChildren >= plan.Policy.ConcurrencyLimit {
			return Dispatch{}, fmt.Errorf("iteration %q has reached its maximum of %d parallel children", definition.ParentTask, plan.Policy.ConcurrencyLimit)
		}
	}
	if err := validateExecutionGrant(request.TaskName, request.Grant); err != nil {
		return Dispatch{}, err
	}
	if request.Grant.Iteration != nil {
		return Dispatch{}, fmt.Errorf("iteration input is harness-owned")
	}
	if len(request.Grant.Dependencies) > 0 {
		return Dispatch{}, fmt.Errorf("dependency snapshots are harness-owned")
	}
	for _, dependency := range definition.DependsOn {
		accepted, exists := h.results[dependency]
		if !exists || h.taskStates[dependency] != TaskCompleted {
			return Dispatch{}, fmt.Errorf("dependency %q has no accepted result", dependency)
		}
		envelope := accepted.Envelope
		request.Grant.Dependencies = append(request.Grant.Dependencies, DependencySnapshot{
			TaskName: dependency, Status: envelope.Status, Summary: envelope.Summary,
			Data:      append(json.RawMessage(nil), envelope.Data...),
			Artifacts: append([]ArtifactReference(nil), envelope.Artifacts...),
		})
	}
	if definition.ParentTask != "" {
		for _, child := range h.iterationPlans[definition.ParentTask].Children {
			if child.Name == request.TaskName {
				request.Grant.Iteration = &IterationInput{ParentTask: definition.ParentTask, Index: child.Index, Item: append(json.RawMessage(nil), child.Item...)}
				break
			}
		}
		if request.Grant.Iteration == nil {
			return Dispatch{}, fmt.Errorf("iteration child %q is absent from its frozen plan", request.TaskName)
		}
	}

	h.taskStates[request.TaskName] = TaskDispatching
	h.attempts[request.TaskName]++
	attempt := h.attempts[request.TaskName]
	dispatchedAt := time.Now().UTC()
	dispatch := Dispatch{
		RunID:         h.runID,
		RequestID:     request.RequestID,
		AttemptID:     fmt.Sprintf("%s:%s:%d", h.runID, request.TaskName, attempt),
		TaskName:      request.TaskName,
		Attempt:       attempt,
		CommanderID:   h.commander.ID,
		StartRevision: h.revision,
		DispatchedAt:  dispatchedAt,
		Grant:         cloneExecutionGrant(request.Grant),
	}
	if definition.ParentTask != "" && attempt == 1 {
		dispatch.StartNotBefore = h.iterationStartNotBeforeLocked(definition.ParentTask, request.TaskName, dispatchedAt)
	}
	h.taskStates[request.TaskName] = TaskRunning
	h.dispatches[request.RequestID] = dispatch
	h.activeByTask[request.TaskName] = dispatch
	h.taskProgress[dispatch.AttemptID] = TaskAttemptProgress{AttemptID: dispatch.AttemptID, Phase: "dispatched", Tools: make(map[string]ToolOperation)}
	h.publishReadySetLocked()
	return cloneDispatch(dispatch), nil
}

func (h *Harness) iterationStartNotBeforeLocked(parent, childName string, now time.Time) time.Time {
	plan := h.iterationPlans[parent]
	if !plan.Policy.Parallel || plan.Policy.StartDelayMS <= 0 {
		return time.Time{}
	}
	if plan.Policy.Smoketest && len(plan.Children) > 0 && childName == plan.Children[0].Name {
		return time.Time{}
	}
	batchSize := min(plan.Policy.ConcurrencyLimit, h.maxParallel)
	if batchSize <= 1 {
		return time.Time{}
	}
	position := 0
	base := now
	for _, child := range plan.Children {
		if plan.Policy.Smoketest && child.Index == 0 {
			continue
		}
		if h.attempts[child.Name] == 0 || child.Name == childName {
			continue
		}
		position++
		for _, prior := range h.dispatches {
			if prior.TaskName == child.Name && !prior.DispatchedAt.IsZero() && prior.DispatchedAt.Before(base) {
				base = prior.DispatchedAt
			}
		}
	}
	if position >= batchSize {
		return time.Time{}
	}
	return base.Add(time.Duration(position*plan.Policy.StartDelayMS) * time.Millisecond)
}

func validateExecutionGrant(taskName string, grant ExecutionGrant) error {
	if grant.TaskName != taskName {
		return fmt.Errorf("execution grant belongs to task %q, want %q", grant.TaskName, taskName)
	}
	if grant.Model == "" {
		return fmt.Errorf("execution grant model is required")
	}
	if grant.RunAs == "" {
		return fmt.Errorf("execution grant run-as identity is required")
	}
	if grant.ConfigurationVersion == "" {
		return fmt.Errorf("execution grant configuration version is required")
	}
	for _, tool := range grant.Tools {
		switch tool {
		case "inspect_mission", "start_task", "inspect_task", "clarify_task_result", "select_route", "finish_mission":
			return fmt.Errorf("executor cannot receive commander control %q", tool)
		}
	}
	return nil
}

func cloneExecutionGrant(grant ExecutionGrant) ExecutionGrant {
	grant.Tools = append([]string(nil), grant.Tools...)
	if grant.Iteration != nil {
		item := *grant.Iteration
		item.Item = append(json.RawMessage(nil), item.Item...)
		grant.Iteration = &item
	}
	dependencies := make([]DependencySnapshot, len(grant.Dependencies))
	for index, dependency := range grant.Dependencies {
		dependency.Data = append(json.RawMessage(nil), dependency.Data...)
		dependency.Artifacts = append([]ArtifactReference(nil), dependency.Artifacts...)
		dependencies[index] = dependency
	}
	grant.Dependencies = dependencies
	return grant
}

func cloneDispatch(dispatch Dispatch) Dispatch {
	dispatch.Grant = cloneExecutionGrant(dispatch.Grant)
	return dispatch
}

func cloneDispatchMap(source map[string]Dispatch) map[string]Dispatch {
	result := make(map[string]Dispatch, len(source))
	for key, dispatch := range source {
		result[key] = cloneDispatch(dispatch)
	}
	return result
}

// CompleteTask accepts the successful terminal result for the active attempt,
// publishes newly eligible tasks, and completes the run when all tasks finish.
func (h *Harness) CompleteTask(taskName, attemptID string, result TaskResultEnvelope) error {
	return h.CompleteTaskWithLease(taskName, attemptID, result, "")
}

func (h *Harness) CompleteTaskWithLease(taskName, attemptID string, result TaskResultEnvelope, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(attemptID, leaseToken); err != nil {
		return err
	}

	active, ok := h.activeByTask[taskName]
	if !ok || active.AttemptID != attemptID {
		return fmt.Errorf("attempt %q is not active for task %q", attemptID, taskName)
	}
	if h.taskStates[taskName] != TaskRunning {
		return fmt.Errorf("task %q is not running", taskName)
	}
	accepted, err := validateTaskResult(active, h.tasks[taskName].ResultPolicy, result)
	if err != nil {
		return &ResultRejectedError{Err: err}
	}
	if err := h.recordTaskTranscriptTurnsLocked(accepted.SessionTranscript); err != nil {
		return &ResultRejectedError{Err: err}
	}
	h.taskStates[taskName] = TaskCompleted
	h.results[taskName] = accepted
	delete(h.failures, taskName)
	delete(h.activeByTask, taskName)
	delete(h.taskProgress, attemptID)
	delete(h.executionLeases, attemptID)
	for _, target := range h.tasks[taskName].SendTo {
		h.activateLocked(target, taskName)
	}
	if parent := h.tasks[taskName].ParentTask; parent != "" {
		h.completeIterationParentLocked(parent)
	}
	h.publishReadySetLocked()

	if h.allCompleteLocked() {
		h.runState = RunCompleted
		h.commander.State = CommanderCompleted
		h.revision++
	}
	return nil
}

// SelectRoute accepts exactly one declared target (or "none") for a completed
// router task. Selection is revision fenced and idempotent by request ID.
func (h *Harness) SelectRoute(request RouteRequest) (RouteSelection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.RequestID == "" {
		return RouteSelection{}, fmt.Errorf("request ID is required")
	}
	for _, previous := range h.routeSelections {
		if previous.RequestID == request.RequestID {
			if previous.TaskName != request.TaskName || previous.Target != request.Target || !reflect.DeepEqual(previous.Inputs, request.Inputs) {
				return RouteSelection{}, fmt.Errorf("route request %q was already used", request.RequestID)
			}
			return previous, nil
		}
	}
	if h.runState != RunRunning || h.commander.State != CommanderActive {
		return RouteSelection{}, fmt.Errorf("run is not accepting route selections")
	}
	if request.Revision != h.revision {
		return RouteSelection{}, fmt.Errorf("stale route revision: got %d, current %d", request.Revision, h.revision)
	}
	definition, ok := h.tasks[request.TaskName]
	if !ok || len(definition.Routes) == 0 {
		return RouteSelection{}, fmt.Errorf("task %q is not a router", request.TaskName)
	}
	if h.taskStates[request.TaskName] != TaskCompleted {
		return RouteSelection{}, fmt.Errorf("router task %q is not completed", request.TaskName)
	}
	if _, exists := h.routeSelections[request.TaskName]; exists {
		return RouteSelection{}, fmt.Errorf("task %q already has a route selection", request.TaskName)
	}
	var selected *RouteDefinition
	if request.Target != "none" {
		for i := range definition.Routes {
			if definition.Routes[i].Target == request.Target {
				selected = &definition.Routes[i]
				break
			}
		}
		if selected == nil {
			return RouteSelection{}, fmt.Errorf("route target %q is not declared for task %q", request.Target, request.TaskName)
		}
	}
	if selected == nil && len(request.Inputs) > 0 {
		return RouteSelection{}, fmt.Errorf("route inputs require a selected mission target")
	}
	if selected != nil && !selected.IsMission && len(request.Inputs) > 0 {
		return RouteSelection{}, fmt.Errorf("local route %q cannot receive mission inputs", selected.Target)
	}
	if selected != nil && selected.IsMission {
		if err := validateCanonicalRouteInputs(*selected, request.Inputs); err != nil {
			return RouteSelection{}, fmt.Errorf("mission %q inputs: %w", selected.Target, err)
		}
	}
	selection := RouteSelection{
		RequestID: request.RequestID, TaskName: request.TaskName, Target: request.Target,
		Inputs: cloneMap(request.Inputs), IsMission: selected != nil && selected.IsMission, Revision: h.revision,
	}
	h.routeSelections[request.TaskName] = selection
	if selection.IsMission {
		h.crossMissionLaunches[request.RequestID] = CrossMissionLaunch{
			ID: request.RequestID, TaskName: request.TaskName, Target: request.Target,
			Inputs: cloneMap(request.Inputs), State: CrossMissionLaunchPending,
		}
	}
	if selected != nil && !selected.IsMission {
		h.activateLocked(selected.Target, request.TaskName)
	}
	h.publishReadySetLocked()
	if h.allCompleteLocked() {
		h.runState = RunCompleted
		h.commander.State = CommanderCompleted
		h.revision++
	}
	return selection, nil
}

func (h *Harness) CompleteCrossMissionLaunch(launchID, childRunID string) (CrossMissionLaunch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if launchID == "" || childRunID == "" {
		return CrossMissionLaunch{}, fmt.Errorf("launch ID and child run ID are required")
	}
	launch, ok := h.crossMissionLaunches[launchID]
	if !ok {
		return CrossMissionLaunch{}, fmt.Errorf("cross-mission launch %q does not exist", launchID)
	}
	if launch.State == CrossMissionLaunchLaunched {
		if launch.ChildRunID == childRunID {
			return cloneCrossMissionLaunch(launch), nil
		}
		return CrossMissionLaunch{}, fmt.Errorf("cross-mission launch %q already created child %q", launchID, launch.ChildRunID)
	}
	if launch.State != CrossMissionLaunchPending {
		return CrossMissionLaunch{}, fmt.Errorf("cross-mission launch %q has invalid state %q", launchID, launch.State)
	}
	launch.State = CrossMissionLaunchLaunched
	launch.ChildRunID = childRunID
	h.crossMissionLaunches[launchID] = launch
	h.revision++
	return cloneCrossMissionLaunch(launch), nil
}

func (h *Harness) activateLocked(target, source string) {
	for _, existing := range h.activations[target] {
		if existing == source {
			return
		}
	}
	h.activations[target] = append(h.activations[target], source)
}

// FailTask durably closes the active attempt. Retryable failures return the
// task to the ready set; an exhausted policy fails the mission. The attempt ID
// fence prevents a late worker from mutating a newer attempt.
func (h *Harness) FailTask(taskName, attemptID string, cause error) (bool, error) {
	return h.FailTaskWithLease(taskName, attemptID, cause, "")
}

func (h *Harness) FailTaskWithLease(taskName, attemptID string, cause error, leaseToken string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.requireExecutionLeaseLocked(attemptID, leaseToken); err != nil {
		return false, err
	}

	active, ok := h.activeByTask[taskName]
	if !ok || active.AttemptID != attemptID {
		return false, fmt.Errorf("attempt %q is not active for task %q", attemptID, taskName)
	}
	if h.taskStates[taskName] != TaskRunning {
		return false, fmt.Errorf("task %q is not running", taskName)
	}
	failure := TaskFailure{AttemptID: attemptID, Attempt: active.Attempt}
	if cause != nil {
		failure.Error = cause.Error()
	}
	h.failures[taskName] = failure
	delete(h.activeByTask, taskName)
	if progress, ok := h.taskProgress[attemptID]; ok {
		progress.Phase = "failed"
		// Tool operation state is not needed after the attempt closes. The
		// transcript preserves the model-visible call and result without
		// retaining operational inputs in an inactive checkpoint entry.
		progress.Tools = nil
		h.taskProgress[attemptID] = progress
	}
	delete(h.executionLeases, attemptID)

	if h.runState == RunRunning && active.Attempt <= h.tasks[taskName].MaxRetries {
		h.taskStates[taskName] = TaskRetryPending
		if seconds := h.tasks[taskName].RetryDelaySeconds; seconds > 0 {
			h.retryDeadlines[taskName] = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		} else {
			h.taskStates[taskName] = TaskReady
		}
		h.revision++
		return true, nil
	}

	h.taskStates[taskName] = TaskFailed
	h.runState = RunFailed
	h.commander.State = CommanderFailed
	h.revision++
	return false, nil
}

func validateRetryDelay(definition TaskDefinition) error {
	if definition.RetryDelaySeconds < 0 || int64(definition.RetryDelaySeconds) > int64((1<<63-1)/time.Second) {
		return fmt.Errorf("task %q retry delay must be a non-negative whole number of seconds", definition.Name)
	}
	return nil
}

// PromoteDueRetries applies persisted deadlines exactly once. A restart may
// call this repeatedly; only tasks still pending at or before now become ready.
func (h *Harness) PromoteDueRetries(now time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runState != RunRunning && h.runState != RunWaiting {
		return nil
	}
	ready := make([]string, 0)
	for taskName, deadline := range h.retryDeadlines {
		if deadline.After(now) {
			continue
		}
		if h.taskStates[taskName] != TaskRetryPending {
			continue
		}
		h.taskStates[taskName] = TaskReady
		delete(h.retryDeadlines, taskName)
		ready = append(ready, taskName)
	}
	if len(ready) > 0 {
		sort.Strings(ready)
		if h.runState == RunWaiting {
			h.runState = RunRunning
			h.commander.State = CommanderActive
		}
		h.revision++
	}
	return ready
}

func (h *Harness) publishReadySetLocked() {
	if h.runState != RunRunning {
		h.revision++
		return
	}
	for name, definition := range h.tasks {
		if h.taskStates[name] != TaskBlocked {
			continue
		}
		if h.isDynamicTargetLocked(name) && len(h.activations[name]) == 0 {
			continue
		}
		ready := true
		for _, dependency := range definition.DependsOn {
			if h.taskStates[dependency] != TaskCompleted {
				ready = false
				break
			}
		}
		if ready {
			h.taskStates[name] = TaskReady
		}
	}
	h.revision++
}

func (h *Harness) snapshotLocked() Snapshot {
	ready := make([]string, 0)
	tasks := make(map[string]TaskSnapshot, len(h.tasks))
	for name := range h.tasks {
		state := h.taskStates[name]
		if state == TaskReady {
			parent := h.tasks[name].ParentTask
			if parent == "" || h.activeIterationChildrenLocked(parent) < h.iterationPlans[parent].Policy.ConcurrencyLimit {
				ready = append(ready, name)
			}
		}
		tasks[name] = TaskSnapshot{Name: name, State: state, Attempt: h.attempts[name]}
	}
	sort.Strings(ready)
	active := make([]Dispatch, 0, len(h.activeByTask))
	for _, dispatch := range h.activeByTask {
		if h.taskStates[dispatch.TaskName] == TaskRunning {
			active = append(active, cloneDispatch(dispatch))
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].TaskName < active[j].TaskName })
	results := make(map[string]TaskResultSummary, len(h.results))
	for taskName, accepted := range h.results {
		results[taskName] = TaskResultSummary{
			Status: accepted.Envelope.Status, Summary: accepted.Envelope.Summary,
			ArtifactCount: len(accepted.Envelope.Artifacts), HasData: len(accepted.Envelope.Data) > 0,
		}
	}
	pendingRoutes := make(map[string][]RouteDefinition)
	for name, definition := range h.tasks {
		if h.taskStates[name] == TaskCompleted && len(definition.Routes) > 0 {
			if _, selected := h.routeSelections[name]; !selected {
				pendingRoutes[name] = append([]RouteDefinition(nil), definition.Routes...)
			}
		}
	}
	pendingClarifications := make([]ClarifyTaskRequest, 0, len(h.pendingClarifications))
	for _, request := range h.pendingClarifications {
		pendingClarifications = append(pendingClarifications, request)
	}
	sort.Slice(pendingClarifications, func(i, j int) bool { return pendingClarifications[i].RequestID < pendingClarifications[j].RequestID })
	return Snapshot{
		RunID:                 h.runID,
		RunState:              h.runState,
		Commander:             h.commander,
		Revision:              h.revision,
		Ready:                 ready,
		Tasks:                 tasks,
		Active:                active,
		Results:               results,
		Failures:              cloneMap(h.failures),
		PendingRoutes:         pendingRoutes,
		RouteSelections:       cloneRouteSelections(h.routeSelections),
		CrossMissionLaunches:  cloneCrossMissionLaunches(h.crossMissionLaunches),
		PendingClarifications: pendingClarifications,
		Clarifications:        cloneClarifications(h.clarifications),
		RecoveryBlocks:        cloneMap(h.recoveryBlocks),
		HumanInputWaits:       cloneHumanInputWaits(h.humanInputWaits),
		HumanInputResponses:   cloneMap(h.humanInputResponses),
		RetryDeadlines:        cloneMap(h.retryDeadlines),
		ModelTurns:            cloneMap(h.modelTurns),
		IterationPlans:        cloneIterationPlans(h.iterationPlans),
		MaxParallel:           h.maxParallel,
		AllComplete:           h.allCompleteLocked(),
	}
}

func (h *Harness) activeIterationChildrenLocked(parent string) int {
	count := 0
	for name := range h.activeByTask {
		if h.tasks[name].ParentTask == parent && (h.taskStates[name] == TaskRunning || h.taskStates[name] == TaskDispatching) {
			count++
		}
	}
	return count
}

func (h *Harness) executableAttemptCountLocked() int {
	count := 0
	for name := range h.activeByTask {
		if h.taskStates[name] == TaskRunning || h.taskStates[name] == TaskDispatching {
			count++
		}
	}
	return count
}

func (h *Harness) checkpointLocked() Checkpoint {
	definitions := make([]TaskDefinition, 0, len(h.tasks))
	for _, definition := range h.tasks {
		definition.DependsOn = append([]string(nil), definition.DependsOn...)
		definition.SendTo = append([]string(nil), definition.SendTo...)
		definition.Routes = append([]RouteDefinition(nil), definition.Routes...)
		definition.ResultPolicy = cloneResultPolicy(definition.ResultPolicy)
		if definition.Iterator != nil {
			policy := *definition.Iterator
			definition.Iterator = &policy
		}
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return Checkpoint{
		RunID:                 h.runID,
		RunState:              h.runState,
		Commander:             h.commander,
		Revision:              h.revision,
		MaxParallel:           h.maxParallel,
		Definitions:           definitions,
		TaskStates:            cloneMap(h.taskStates),
		Attempts:              cloneMap(h.attempts),
		Dispatches:            cloneDispatchMap(h.dispatches),
		ActiveByTask:          cloneDispatchMap(h.activeByTask),
		TaskProgress:          cloneTaskProgressMap(h.taskProgress),
		ExecutionLeases:       cloneMap(h.executionLeases),
		RecoveryBlocks:        cloneMap(h.recoveryBlocks),
		HumanInputWaits:       cloneHumanInputWaits(h.humanInputWaits),
		HumanInputResponses:   cloneMap(h.humanInputResponses),
		RetryDeadlines:        cloneMap(h.retryDeadlines),
		Results:               cloneAcceptedResults(h.results),
		Failures:              cloneMap(h.failures),
		Activations:           cloneStringSliceMap(h.activations),
		RouteSelections:       cloneRouteSelections(h.routeSelections),
		CrossMissionLaunches:  cloneCrossMissionLaunches(h.crossMissionLaunches),
		PendingClarifications: cloneMap(h.pendingClarifications),
		Clarifications:        cloneClarifications(h.clarifications),
		CommanderMessages:     cloneCommanderMessages(h.commanderMessages),
		ModelTurns:            cloneMap(h.modelTurns),
		IterationPlans:        cloneIterationPlans(h.iterationPlans),
	}
}

func (h *Harness) recordTaskTranscriptTurnsLocked(messages []TaskSessionMessage) error {
	for _, message := range messages {
		if message.Telemetry == nil {
			continue
		}
		if err := h.recordModelTurnLocked(*message.Telemetry); err != nil {
			return err
		}
	}
	return nil
}

func (h *Harness) recordModelTurnLocked(turn ModelTurnTelemetry) error {
	if turn.ID == "" || turn.SessionID == "" || turn.Entity == "" {
		return fmt.Errorf("model turn telemetry requires id, session, and entity")
	}
	if previous, ok := h.modelTurns[turn.ID]; ok {
		if reflect.DeepEqual(previous, turn) {
			return nil
		}
		return fmt.Errorf("model turn %q already exists with different telemetry", turn.ID)
	}
	h.modelTurns[turn.ID] = turn
	return nil
}

func cloneTaskClarification(value TaskClarification) TaskClarification {
	value.Answer = append([]store.MessagePart(nil), value.Answer...)
	return value
}

func cloneIterationPlan(plan IterationPlan) IterationPlan {
	children := make([]IterationChild, len(plan.Children))
	for index, child := range plan.Children {
		child.Item = append(json.RawMessage(nil), child.Item...)
		child.DependsOn = append([]string(nil), child.DependsOn...)
		children[index] = child
	}
	plan.Children = children
	return plan
}

func cloneHumanInputWait(wait HumanInputWait) HumanInputWait {
	wait.Choices = append([]string(nil), wait.Choices...)
	if wait.DeadlineAt != nil {
		deadline := *wait.DeadlineAt
		wait.DeadlineAt = &deadline
	}
	return wait
}

func cloneHumanInputWaits(source map[string]HumanInputWait) map[string]HumanInputWait {
	result := make(map[string]HumanInputWait, len(source))
	for attemptID, wait := range source {
		result[attemptID] = cloneHumanInputWait(wait)
	}
	return result
}

func cloneIterationPlans(source map[string]IterationPlan) map[string]IterationPlan {
	result := make(map[string]IterationPlan, len(source))
	for name, plan := range source {
		result[name] = cloneIterationPlan(plan)
	}
	return result
}

func cloneRouteSelections(source map[string]RouteSelection) map[string]RouteSelection {
	result := make(map[string]RouteSelection, len(source))
	for key, selection := range source {
		selection.Inputs = cloneMap(selection.Inputs)
		result[key] = selection
	}
	return result
}

func cloneCrossMissionLaunch(value CrossMissionLaunch) CrossMissionLaunch {
	value.Inputs = cloneMap(value.Inputs)
	return value
}

func cloneCrossMissionLaunches(source map[string]CrossMissionLaunch) map[string]CrossMissionLaunch {
	result := make(map[string]CrossMissionLaunch, len(source))
	for key, launch := range source {
		result[key] = cloneCrossMissionLaunch(launch)
	}
	return result
}

func cloneTaskProgress(value TaskAttemptProgress) TaskAttemptProgress {
	value.Transcript = cloneTaskSessionMessages(value.Transcript)
	value.Tools = cloneMap(value.Tools)
	return value
}

func cloneTaskProgressMap(source map[string]TaskAttemptProgress) map[string]TaskAttemptProgress {
	result := make(map[string]TaskAttemptProgress, len(source))
	for key, value := range source {
		result[key] = cloneTaskProgress(value)
	}
	return result
}

func cloneClarifications(source map[string][]TaskClarification) map[string][]TaskClarification {
	result := make(map[string][]TaskClarification, len(source))
	for taskName, values := range source {
		items := make([]TaskClarification, len(values))
		for index, value := range values {
			items[index] = cloneTaskClarification(value)
		}
		result[taskName] = items
	}
	return result
}

func (h *Harness) allCompleteLocked() bool {
	for name, definition := range h.tasks {
		if h.taskStates[name] == TaskCompleted {
			if len(definition.Routes) > 0 {
				if _, selected := h.routeSelections[name]; !selected {
					return false
				}
			}
			continue
		}
		if !h.isDynamicTargetLocked(name) || len(h.activations[name]) > 0 {
			return false
		}
	}
	return true
}

func (h *Harness) isDynamicTargetLocked(taskName string) bool {
	for _, definition := range h.tasks {
		for _, target := range definition.SendTo {
			if target == taskName {
				return true
			}
		}
		for _, route := range definition.Routes {
			if !route.IsMission && route.Target == taskName {
				return true
			}
		}
	}
	return false
}

func validateAcyclic(tasks map[string]TaskDefinition) error {
	const (
		unseen = iota
		visiting
		visited
	)
	marks := make(map[string]int, len(tasks))
	var visit func(string) error
	visit = func(name string) error {
		switch marks[name] {
		case visiting:
			return fmt.Errorf("mission graph contains a cycle at task %q", name)
		case visited:
			return nil
		}
		marks[name] = visiting
		for _, dependency := range tasks[name].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		for _, target := range tasks[name].SendTo {
			if err := visit(target); err != nil {
				return err
			}
		}
		for _, route := range tasks[name].Routes {
			if !route.IsMission {
				if err := visit(route.Target); err != nil {
					return err
				}
			}
		}
		marks[name] = visited
		return nil
	}
	for name := range tasks {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringSliceMap(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source))
	for key, values := range source {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func cloneCommanderMessages(source []CommanderMessage) []CommanderMessage {
	result := make([]CommanderMessage, len(source))
	for index, message := range source {
		message.Parts = append([]store.MessagePart(nil), message.Parts...)
		if message.Telemetry != nil {
			telemetry := *message.Telemetry
			message.Telemetry = &telemetry
		}
		result[index] = message
	}
	return result
}
