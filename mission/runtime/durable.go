package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"squadron/store"
)

// EventType names a persisted mission-runtime event.
type EventType string

const (
	EventMissionCreated              EventType = "mission.created"
	EventMissionStarted              EventType = "mission.started"
	EventMissionStopped              EventType = "mission.stopped"
	EventMissionResumed              EventType = "mission.resumed"
	EventMissionCompleted            EventType = "mission.completed"
	EventReadySetPublished           EventType = "ready_set.published"
	EventTaskDispatched              EventType = "task.dispatched"
	EventTaskLeaseAcquired           EventType = "task.lease_acquired"
	EventTaskLeaseReleased           EventType = "task.lease_released"
	EventTaskProgressed              EventType = "task.progressed"
	EventTaskRecoveryBlocked         EventType = "task.recovery_blocked"
	EventTaskRecoveryResolved        EventType = "task.recovery_resolved"
	EventHumanInputRequested         EventType = "human_input.requested"
	EventHumanInputResolved          EventType = "human_input.resolved"
	EventMissionHumanInputWaiting    EventType = "mission.human_input_waiting"
	EventMissionRecoveryWaiting      EventType = "mission.recovery_waiting"
	EventTaskCompleted               EventType = "task.completed"
	EventIterationExpanded           EventType = "iteration.expanded"
	EventTaskAttemptFailed           EventType = "task.attempt_failed"
	EventTaskRetryScheduled          EventType = "task.retry_scheduled"
	EventTaskFailed                  EventType = "task.failed"
	EventMissionFailed               EventType = "mission.failed"
	EventRouteSelected               EventType = "route.selected"
	EventCrossMissionLaunchRequested EventType = "cross_mission_launch.requested"
	EventCrossMissionLaunchCompleted EventType = "cross_mission_launch.completed"
	EventTaskInspected               EventType = "task.inspected"
	EventTaskClarificationRequested  EventType = "task.clarification_requested"
	EventTaskClarified               EventType = "task.clarified"
	EventCommanderMessageAppended    EventType = "commander.message_appended"
)

// Event is the append-only audit record committed with a checkpoint.
type Event struct {
	Sequence      uint64
	RunID         string
	Revision      uint64
	Type          EventType
	OccurredAt    time.Time
	CorrelationID string
	TaskName      string
	AttemptID     string
	RequestID     string
	Result        *AcceptedTaskResult
	Failure       *TaskFailure
	Route         *RouteSelection
	ProgressPhase string
	Recovery      *RecoveryBlock
	HumanInput    *HumanInputWait
	HumanAnswer   *HumanInputResponse
	Launch        *CrossMissionLaunch
	Turns         []ModelTurnTelemetry
	IterationPlan *IterationPlan
}

// BeginCommanderClarification atomically records the commander's control
// turn and a pending clarification before any provider call is made.
func (h *DurableHarness) BeginCommanderClarification(prompt, message CommanderMessage, request ClarifyTaskRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	if previous, ok := before.PendingClarifications[request.RequestID]; ok {
		if previous == request {
			return nil
		}
		return fmt.Errorf("clarification request %q already exists", request.RequestID)
	}
	for _, completed := range before.Clarifications {
		for _, clarification := range completed {
			if clarification.RequestID == request.RequestID {
				return nil
			}
		}
	}
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := validateClarifyMessage(message, request); err != nil {
		return err
	}
	if err := candidate.BeginClarification(request); err != nil {
		return err
	}
	if err := candidate.AppendCommanderMessage(prompt); err != nil {
		return err
	}
	if err := candidate.AppendCommanderMessage(message); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: prompt.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: message.ID, Turns: messageTurns(message)},
		{RunID: after.RunID, Revision: after.Revision, Type: EventTaskClarificationRequested, OccurredAt: time.Now().UTC(), CorrelationID: request.RequestID, TaskName: request.TaskName, RequestID: request.RequestID},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task clarification request: %w", err)
	}
	h.inner = candidate
	return nil
}

func validateClarifyMessage(message CommanderMessage, request ClarifyTaskRequest) error {
	for _, part := range message.Parts {
		if part.Type == "tool_use" && part.ToolUseID == request.RequestID && part.ToolName == "clarify_task_result" {
			return nil
		}
	}
	return fmt.Errorf("commander message does not contain matching clarify_task_result call %q", request.RequestID)
}

func (h *DurableHarness) ClarificationContext(requestID string) (ClarifyTaskRequest, Dispatch, []TaskSessionMessage, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inner.ClarificationContext(requestID)
}

// CompleteClarification commits the supplemental answer, updated worker
// transcript, and commander tool result as one atomic transition.
func (h *DurableHarness) CompleteClarification(requestID, messageID string, result TaskClarificationResult) (TaskClarification, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	if _, pending := before.PendingClarifications[requestID]; !pending {
		for _, completed := range before.Clarifications {
			for _, clarification := range completed {
				if clarification.RequestID == requestID {
					return cloneTaskClarification(clarification), nil
				}
			}
		}
	}
	candidate, err := RestoreHarness(before)
	if err != nil {
		return TaskClarification{}, err
	}
	clarification, err := candidate.CompleteClarification(requestID, result)
	if err != nil {
		return TaskClarification{}, err
	}
	payload, err := json.Marshal(clarification)
	if err != nil {
		return TaskClarification{}, fmt.Errorf("encode task clarification: %w", err)
	}
	ack := CommanderMessage{ID: messageID + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: requestID, Text: string(payload)}}}
	if err := candidate.AppendCommanderMessage(ack); err != nil {
		return TaskClarification{}, err
	}
	after := candidate.Checkpoint()
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: ack.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventTaskClarified, OccurredAt: time.Now().UTC(), CorrelationID: requestID, TaskName: clarification.TaskName, RequestID: requestID, Turns: newModelTurns(before.ModelTurns, after.ModelTurns)},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return TaskClarification{}, fmt.Errorf("persisting task clarification: %w", err)
	}
	h.inner = candidate
	return clarification, nil
}

// ApplyCommanderInspection atomically records a read-only inspection turn and
// its harness-produced result without exposing worker transcripts or telemetry.
func (h *DurableHarness) ApplyCommanderInspection(prompt, message CommanderMessage, request InspectTaskRequest) (TaskInspection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return TaskInspection{}, err
	}
	if err := validateInspectMessage(message, request); err != nil {
		return TaskInspection{}, err
	}
	inspection, err := candidate.InspectTask(request)
	if err != nil {
		return TaskInspection{}, err
	}
	if err := candidate.AppendCommanderMessage(prompt); err != nil {
		return TaskInspection{}, err
	}
	if err := candidate.AppendCommanderMessage(message); err != nil {
		return TaskInspection{}, err
	}
	payload, err := json.Marshal(inspection)
	if err != nil {
		return TaskInspection{}, fmt.Errorf("encode task inspection: %w", err)
	}
	ack := CommanderMessage{ID: message.ID + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: request.RequestID, Text: string(payload)}}}
	if err := candidate.AppendCommanderMessage(ack); err != nil {
		return TaskInspection{}, err
	}
	after := candidate.Checkpoint()
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: prompt.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: message.ID, Turns: messageTurns(message)},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: ack.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventTaskInspected, OccurredAt: time.Now().UTC(), CorrelationID: request.RequestID, TaskName: request.TaskName, RequestID: request.RequestID},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return TaskInspection{}, fmt.Errorf("persisting task inspection: %w", err)
	}
	h.inner = candidate
	return inspection, nil
}

func validateInspectMessage(message CommanderMessage, request InspectTaskRequest) error {
	for _, part := range message.Parts {
		if part.Type == "tool_use" && part.ToolUseID == request.RequestID && part.ToolName == "inspect_task" {
			return nil
		}
	}
	return fmt.Errorf("commander message does not contain matching inspect_task call %q", request.RequestID)
}

func (h *DurableHarness) AcquireExecutionLease(attemptID, ownerID, token string, now time.Time, ttl time.Duration) (ExecutionLease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return ExecutionLease{}, err
	}
	lease, err := candidate.AcquireExecutionLease(attemptID, ownerID, token, now, ttl)
	if err != nil {
		return ExecutionLease{}, err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return lease, nil
	}
	events := []Event{{RunID: after.RunID, Revision: after.Revision, Type: EventTaskLeaseAcquired, OccurredAt: time.Now().UTC(), AttemptID: attemptID, CorrelationID: token}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return ExecutionLease{}, fmt.Errorf("persisting task execution lease: %w", err)
	}
	h.inner = candidate
	return lease, nil
}

func (h *DurableHarness) RenewExecutionLease(attemptID, token string, now time.Time, ttl time.Duration) (ExecutionLease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return ExecutionLease{}, err
	}
	lease, err := candidate.RenewExecutionLease(attemptID, token, now, ttl)
	if err != nil {
		return ExecutionLease{}, err
	}
	after := candidate.Checkpoint()
	// Renewals are checkpointed but intentionally do not create high-volume
	// audit events. Lease acquisition and release remain visible boundaries.
	if err := h.commit(before.Revision, after, nil); err != nil {
		return ExecutionLease{}, fmt.Errorf("persisting task execution lease renewal: %w", err)
	}
	h.inner = candidate
	return lease, nil
}

func (h *DurableHarness) ReleaseExecutionLease(attemptID, token string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.ReleaseExecutionLease(attemptID, token); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{{RunID: after.RunID, Revision: after.Revision, Type: EventTaskLeaseReleased, OccurredAt: time.Now().UTC(), AttemptID: attemptID, CorrelationID: token}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task execution lease release: %w", err)
	}
	h.inner = candidate
	return nil
}

// FailTask durably records an executor failure and either schedules another
// attempt or moves the task and mission to their terminal failed states.
func (h *DurableHarness) FailTask(taskName, attemptID string, cause error) (bool, error) {
	return h.FailTaskWithLease(taskName, attemptID, cause, "")
}

func (h *DurableHarness) FailTaskWithLease(taskName, attemptID string, cause error, leaseToken string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return false, err
	}
	retry, err := candidate.FailTaskWithLease(taskName, attemptID, cause, leaseToken)
	if err != nil {
		return false, err
	}
	after := candidate.Checkpoint()
	failure := after.Failures[taskName]
	events := []Event{{RunID: after.RunID, Revision: after.Revision, Type: EventTaskAttemptFailed, OccurredAt: time.Now().UTC(), TaskName: taskName, AttemptID: attemptID, Failure: &failure}}
	if retry {
		events = append(events,
			Event{RunID: after.RunID, Revision: after.Revision, Type: EventTaskRetryScheduled, OccurredAt: time.Now().UTC(), TaskName: taskName, AttemptID: attemptID},
			Event{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
		)
	} else {
		events = append(events,
			Event{RunID: after.RunID, Revision: after.Revision, Type: EventTaskFailed, OccurredAt: time.Now().UTC(), TaskName: taskName, AttemptID: attemptID},
			Event{RunID: after.RunID, Revision: after.Revision, Type: EventMissionFailed, OccurredAt: time.Now().UTC()},
		)
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return false, fmt.Errorf("persisting task failure: %w", err)
	}
	h.inner = candidate
	return retry, nil
}

// TaskResult returns the full validated result without placing it in every
// commander snapshot.
func (h *DurableHarness) TaskResult(taskName string) (AcceptedTaskResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inner.TaskResult(taskName)
}

func (h *DurableHarness) TaskProgress(attemptID string) (TaskAttemptProgress, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inner.TaskProgress(attemptID)
}

func (h *DurableHarness) SaveTaskProgress(dispatch Dispatch, progress TaskAttemptProgress) error {
	return h.SaveTaskProgressWithLease(dispatch, progress, "")
}

func (h *DurableHarness) SaveTaskProgressWithLease(dispatch Dispatch, progress TaskAttemptProgress, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.SaveTaskProgressWithLease(dispatch, progress, leaseToken); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	committed := after.TaskProgress[dispatch.AttemptID]
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventTaskProgressed,
		OccurredAt: time.Now().UTC(), TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID,
		CorrelationID: dispatch.AttemptID, ProgressPhase: committed.Phase, Turns: newModelTurns(before.ModelTurns, after.ModelTurns),
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task progress: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) BlockTaskRecovery(dispatch Dispatch, block RecoveryBlock) error {
	return h.BlockTaskRecoveryWithLease(dispatch, block, "")
}

func (h *DurableHarness) BlockTaskRecoveryWithLease(dispatch Dispatch, block RecoveryBlock, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.BlockTaskRecoveryWithLease(dispatch, block, leaseToken); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	committed := after.RecoveryBlocks[dispatch.AttemptID]
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventTaskRecoveryBlocked, OccurredAt: time.Now().UTC(),
		TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID, CorrelationID: block.ToolUseID, Recovery: &committed,
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task recovery block: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) WaitTaskForHumanInputWithLease(dispatch Dispatch, wait HumanInputWait, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.WaitTaskForHumanInputWithLease(dispatch, wait, leaseToken); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	committed := after.HumanInputWaits[dispatch.AttemptID]
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventHumanInputRequested, OccurredAt: time.Now().UTC(),
		TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID, CorrelationID: wait.ToolCallID, HumanInput: &committed,
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting human input wait: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) ResolveHumanInputWait(attemptID, toolCallID, response, responder string, isError bool, resolvedAt time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.ResolveHumanInputWait(attemptID, toolCallID, response, responder, isError, resolvedAt); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	answer := after.HumanInputResponses[toolCallID]
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventHumanInputResolved, OccurredAt: resolvedAt,
		AttemptID: attemptID, CorrelationID: toolCallID, HumanAnswer: &answer,
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting human input resolution: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) EnterRecoveryWaiting() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.EnterRecoveryWaiting(); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	eventType := EventMissionRecoveryWaiting
	if len(after.HumanInputWaits) > 0 && len(after.RecoveryBlocks) == 0 {
		eventType = EventMissionHumanInputWaiting
	}
	events := []Event{{RunID: after.RunID, Revision: after.Revision, Type: eventType, OccurredAt: time.Now().UTC()}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting mission recovery wait: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) ResolveRecoveryBlock(attemptID, result string, isError bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	block, ok := before.RecoveryBlocks[attemptID]
	if !ok {
		return fmt.Errorf("attempt %q has no recovery block", attemptID)
	}
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.ResolveRecoveryBlock(attemptID, result, isError); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventTaskRecoveryResolved, OccurredAt: time.Now().UTC(),
		TaskName: block.TaskName, AttemptID: attemptID, CorrelationID: block.ToolUseID, Recovery: &block,
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task recovery resolution: %w", err)
	}
	h.inner = candidate
	return nil
}

func (h *DurableHarness) InspectTask(request InspectTaskRequest) (TaskInspection, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inner.InspectTask(request)
}

// Stop durably pauses a quiescent run while preserving its commander session.
func (h *DurableHarness) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.Stop(); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{{RunID: after.RunID, Revision: after.Revision, Type: EventMissionStopped, OccurredAt: time.Now().UTC()}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting mission stop: %w", err)
	}
	h.inner = candidate
	return nil
}

// Resume durably reactivates the same commander session and republishes the
// ready set without replaying external work.
func (h *DurableHarness) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.Resume(); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventMissionResumed, OccurredAt: time.Now().UTC()},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting mission resume: %w", err)
	}
	h.inner = candidate
	return nil
}

// AppendCommanderMessage durably appends one structured commander message.
func (h *DurableHarness) AppendCommanderMessage(message CommanderMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.AppendCommanderMessage(message); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	events := []Event{{
		RunID:         after.RunID,
		Revision:      after.Revision,
		Type:          EventCommanderMessageAppended,
		OccurredAt:    time.Now().UTC(),
		CorrelationID: message.ID,
		Turns:         messageTurns(message),
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting commander message: %w", err)
	}
	h.inner = candidate
	return nil
}

// ApplyCommanderDispatch atomically records the commander's structured turn
// and accepts its task dispatch. A crash cannot leave a visible decision
// without the corresponding state transition (or vice versa).
func (h *DurableHarness) ApplyCommanderDispatch(prompt, message CommanderMessage, request DispatchRequest) (Dispatch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return Dispatch{}, err
	}
	if err := validateStartTaskMessage(message, request); err != nil {
		return Dispatch{}, err
	}
	dispatch, err := candidate.DispatchTask(request)
	if err != nil {
		return Dispatch{}, err
	}
	if err := candidate.AppendCommanderMessage(prompt); err != nil {
		return Dispatch{}, err
	}
	if err := candidate.AppendCommanderMessage(message); err != nil {
		return Dispatch{}, err
	}
	acknowledgement := dispatchAcknowledgement(message.ID, request.RequestID, dispatch)
	if err := candidate.AppendCommanderMessage(acknowledgement); err != nil {
		return Dispatch{}, err
	}
	after := candidate.Checkpoint()
	// Replaying a fully committed commander turn is a no-op. Every constituent
	// operation is idempotent, so an unchanged revision means the dispatch and
	// all three transcript messages already exist. Do not append a second event
	// batch for the same logical turn after process recovery.
	if after.Revision == before.Revision {
		return dispatch, nil
	}
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: prompt.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: message.ID, Turns: messageTurns(message)},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: acknowledgement.ID},
		{
			RunID: after.RunID, Revision: after.Revision, Type: EventTaskDispatched, OccurredAt: time.Now().UTC(),
			CorrelationID: request.RequestID, TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID, RequestID: request.RequestID,
		},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return Dispatch{}, fmt.Errorf("persisting commander dispatch: %w", err)
	}
	h.inner = candidate
	return dispatch, nil
}

func validateStartTaskMessage(message CommanderMessage, request DispatchRequest) error {
	for _, part := range message.Parts {
		if part.Type == "tool_use" && part.ToolUseID == request.RequestID && part.ToolName == "start_task" {
			return nil
		}
	}
	return fmt.Errorf("commander message does not contain matching start_task call %q", request.RequestID)
}

func dispatchAcknowledgement(messageID, toolUseID string, dispatch Dispatch) CommanderMessage {
	return CommanderMessage{
		ID: messageID + ":result", Role: "user",
		Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: toolUseID, Text: dispatchResultText(dispatch)}},
	}
}

func dispatchResultText(dispatch Dispatch) string {
	payload, _ := json.Marshal(map[string]any{
		"accepted": true, "task": dispatch.TaskName, "attempt_id": dispatch.AttemptID,
	})
	return string(payload)
}

// CommanderMessages returns the latest committed structured conversation.
func (h *DurableHarness) CommanderMessages() []CommanderMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inner.CommanderMessages()
}

// DurableHarness adds write-ahead durability to Harness. Each operation is
// first applied to an isolated candidate, atomically committed, and only then
// made visible in memory.
type DurableHarness struct {
	mu    sync.RWMutex
	store store.RuntimeStore
	inner *Harness
}

// CreateDurableHarness creates and persists a pending mission run.
func CreateDurableHarness(runtimeStore store.RuntimeStore, runID, commanderSessionID string, definitions []TaskDefinition) (*DurableHarness, error) {
	return CreateDurableHarnessWithMaxParallel(runtimeStore, runID, commanderSessionID, definitions, 1)
}

func CreateDurableHarnessWithMaxParallel(runtimeStore store.RuntimeStore, runID, commanderSessionID string, definitions []TaskDefinition, maxParallel int) (*DurableHarness, error) {
	if runtimeStore == nil {
		return nil, fmt.Errorf("durable store is required")
	}
	inner, err := NewHarnessWithMaxParallel(runID, commanderSessionID, definitions, maxParallel)
	if err != nil {
		return nil, err
	}
	checkpoint := inner.Checkpoint()
	events := []Event{{
		RunID:      runID,
		Revision:   checkpoint.Revision,
		Type:       EventMissionCreated,
		OccurredAt: time.Now().UTC(),
	}}
	checkpointRecord, eventRecords, err := encodeCommit(checkpoint, events)
	if err != nil {
		return nil, err
	}
	if err := runtimeStore.CreateRuntime(checkpointRecord, eventRecords); err != nil {
		return nil, fmt.Errorf("persisting mission creation: %w", err)
	}
	return &DurableHarness{store: runtimeStore, inner: inner}, nil
}

// RestoreDurableHarness restores the latest committed checkpoint and event
// history without invoking external work.
func RestoreDurableHarness(runtimeStore store.RuntimeStore, runID string) (*DurableHarness, []Event, error) {
	if runtimeStore == nil {
		return nil, nil, fmt.Errorf("durable store is required")
	}
	checkpointRecord, eventRecords, err := runtimeStore.LoadRuntime(runID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading mission run %q: %w", runID, err)
	}
	checkpoint, events, err := decodeCommit(checkpointRecord, eventRecords)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding mission run %q: %w", runID, err)
	}
	if err := validateEventSequence(runID, events); err != nil {
		return nil, nil, err
	}
	inner, err := RestoreHarness(checkpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("restoring mission run %q: %w", runID, err)
	}
	return &DurableHarness{store: runtimeStore, inner: inner}, events, nil
}

// Snapshot returns the latest committed projection.
func (h *DurableHarness) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inner.Snapshot()
}

// Start durably starts the run and publishes its initial ready set.
func (h *DurableHarness) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.Start(); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventMissionStarted, OccurredAt: time.Now().UTC()},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting mission start: %w", err)
	}
	h.inner = candidate
	return nil
}

// ExpandIteration atomically commits the frozen plan, child definitions, and
// ready set. A replay of the same plan performs no second write.
func (h *DurableHarness) ExpandIteration(plan IterationPlan) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.ExpandIteration(plan); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return nil
	}
	frozen := cloneIterationPlan(after.IterationPlans[plan.ParentTask])
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventIterationExpanded, OccurredAt: time.Now().UTC(), TaskName: plan.ParentTask, IterationPlan: &frozen},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if accepted, completed := after.Results[plan.ParentTask]; completed {
		events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventTaskCompleted, OccurredAt: time.Now().UTC(), TaskName: plan.ParentTask, Result: &accepted})
	}
	if after.RunState == RunCompleted {
		events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventMissionCompleted, OccurredAt: time.Now().UTC()})
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting iteration expansion: %w", err)
	}
	h.inner = candidate
	return nil
}

// DispatchTask durably accepts one commander dispatch request.
func (h *DurableHarness) DispatchTask(request DispatchRequest) (Dispatch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return Dispatch{}, err
	}
	dispatch, err := candidate.DispatchTask(request)
	if err != nil {
		return Dispatch{}, err
	}
	after := candidate.Checkpoint()
	// An idempotent retry returns the previously committed dispatch without
	// changing the projection, so it requires no second persistence write.
	if after.Revision == before.Revision {
		return dispatch, nil
	}
	events := []Event{
		{
			RunID:         after.RunID,
			Revision:      after.Revision,
			Type:          EventTaskDispatched,
			OccurredAt:    time.Now().UTC(),
			CorrelationID: request.RequestID,
			TaskName:      dispatch.TaskName,
			AttemptID:     dispatch.AttemptID,
			RequestID:     request.RequestID,
		},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return Dispatch{}, fmt.Errorf("persisting task dispatch: %w", err)
	}
	h.inner = candidate
	return dispatch, nil
}

// CompleteTask durably accepts a successful terminal task result.
func (h *DurableHarness) CompleteTask(taskName, attemptID string, result TaskResultEnvelope) error {
	return h.CompleteTaskWithLease(taskName, attemptID, result, "")
}

func (h *DurableHarness) CompleteTaskWithLease(taskName, attemptID string, result TaskResultEnvelope, leaseToken string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return err
	}
	if err := candidate.CompleteTaskWithLease(taskName, attemptID, result, leaseToken); err != nil {
		return err
	}
	after := candidate.Checkpoint()
	accepted := after.Results[taskName]
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventTaskCompleted, OccurredAt: time.Now().UTC(), TaskName: taskName, AttemptID: attemptID, Result: &accepted},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if parent := checkpointDefinitionParent(after, taskName); parent != "" {
		if _, wasCompleted := before.Results[parent]; !wasCompleted {
			if parentResult, completed := after.Results[parent]; completed {
				events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventTaskCompleted, OccurredAt: time.Now().UTC(), TaskName: parent, Result: &parentResult})
			}
		}
	}
	if after.RunState == RunCompleted {
		events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventMissionCompleted, OccurredAt: time.Now().UTC()})
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return fmt.Errorf("persisting task completion: %w", err)
	}
	h.inner = candidate
	return nil
}

func checkpointDefinitionParent(checkpoint Checkpoint, taskName string) string {
	for _, definition := range checkpoint.Definitions {
		if definition.Name == taskName {
			return definition.ParentTask
		}
	}
	return ""
}

// ApplyCommanderRoute atomically records the commander's route-control turn
// and the harness-validated selection.
func (h *DurableHarness) ApplyCommanderRoute(prompt, message CommanderMessage, request RouteRequest) (RouteSelection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return RouteSelection{}, err
	}
	if err := validateRouteMessage(message, request); err != nil {
		return RouteSelection{}, err
	}
	probe, err := RestoreHarness(before)
	if err != nil {
		return RouteSelection{}, err
	}
	validated, err := probe.SelectRoute(request)
	if err != nil {
		return RouteSelection{}, err
	}
	if err := candidate.AppendCommanderMessage(prompt); err != nil {
		return RouteSelection{}, err
	}
	if err := candidate.AppendCommanderMessage(message); err != nil {
		return RouteSelection{}, err
	}
	ack := routeAcknowledgement(message.ID, request.RequestID, validated)
	if err := candidate.AppendCommanderMessage(ack); err != nil {
		return RouteSelection{}, err
	}
	request.Revision = candidate.Snapshot().Revision
	selection, err := candidate.SelectRoute(request)
	if err != nil {
		return RouteSelection{}, err
	}
	after := candidate.Checkpoint()
	// As with dispatch, a restored process may replay the last provider turn.
	// If its transcript and route selection are already committed, preserve the
	// original selection without creating duplicate audit/launch events.
	if after.Revision == before.Revision {
		return selection, nil
	}
	events := []Event{
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: prompt.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: message.ID, Turns: messageTurns(message)},
		{RunID: after.RunID, Revision: after.Revision, Type: EventCommanderMessageAppended, OccurredAt: time.Now().UTC(), CorrelationID: ack.ID},
		{RunID: after.RunID, Revision: after.Revision, Type: EventRouteSelected, OccurredAt: time.Now().UTC(), CorrelationID: request.RequestID, TaskName: request.TaskName, RequestID: request.RequestID, Route: &selection},
		{RunID: after.RunID, Revision: after.Revision, Type: EventReadySetPublished, OccurredAt: time.Now().UTC()},
	}
	if selection.IsMission {
		launch := after.CrossMissionLaunches[selection.RequestID]
		events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventCrossMissionLaunchRequested, OccurredAt: time.Now().UTC(), CorrelationID: launch.ID, TaskName: launch.TaskName, RequestID: launch.ID, Launch: &launch})
	}
	if after.RunState == RunCompleted {
		events = append(events, Event{RunID: after.RunID, Revision: after.Revision, Type: EventMissionCompleted, OccurredAt: time.Now().UTC()})
	}
	if err := h.commit(before.Revision, after, events); err != nil {
		return RouteSelection{}, fmt.Errorf("persisting route selection: %w", err)
	}
	h.inner = candidate
	return selection, nil
}

func (h *DurableHarness) CompleteCrossMissionLaunch(launchID, childRunID string) (CrossMissionLaunch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.inner.Checkpoint()
	candidate, err := RestoreHarness(before)
	if err != nil {
		return CrossMissionLaunch{}, err
	}
	launch, err := candidate.CompleteCrossMissionLaunch(launchID, childRunID)
	if err != nil {
		return CrossMissionLaunch{}, err
	}
	after := candidate.Checkpoint()
	if after.Revision == before.Revision {
		return launch, nil
	}
	events := []Event{{
		RunID: after.RunID, Revision: after.Revision, Type: EventCrossMissionLaunchCompleted,
		OccurredAt: time.Now().UTC(), CorrelationID: launch.ID, TaskName: launch.TaskName,
		RequestID: launch.ID, Launch: &launch,
	}}
	if err := h.commit(before.Revision, after, events); err != nil {
		return CrossMissionLaunch{}, fmt.Errorf("persisting cross-mission launch completion: %w", err)
	}
	h.inner = candidate
	return launch, nil
}

func validateRouteMessage(message CommanderMessage, request RouteRequest) error {
	for _, part := range message.Parts {
		if part.Type == "tool_use" && part.ToolUseID == request.RequestID && part.ToolName == "select_route" {
			return nil
		}
	}
	return fmt.Errorf("commander message does not contain matching select_route call %q", request.RequestID)
}

func routeAcknowledgement(messageID, toolUseID string, selection RouteSelection) CommanderMessage {
	payload, _ := json.Marshal(map[string]any{"accepted": true, "task": selection.TaskName, "target": selection.Target})
	return CommanderMessage{ID: messageID + ":result", Role: "user", Parts: []store.MessagePart{{Type: "tool_result", ToolUseID: toolUseID, Text: string(payload)}}}
}

func validateEventSequence(runID string, events []Event) error {
	for index, event := range events {
		want := uint64(index + 1)
		if event.RunID != runID {
			return fmt.Errorf("event %d belongs to run %q, want %q", event.Sequence, event.RunID, runID)
		}
		if event.Sequence != want {
			return fmt.Errorf("event sequence gap for run %q: got %d, want %d", runID, event.Sequence, want)
		}
	}
	return nil
}

func messageTurns(message CommanderMessage) []ModelTurnTelemetry {
	if message.Telemetry == nil {
		return nil
	}
	return []ModelTurnTelemetry{*message.Telemetry}
}

func newModelTurns(before, after map[string]ModelTurnTelemetry) []ModelTurnTelemetry {
	ids := make([]string, 0)
	for id := range after {
		if _, existed := before[id]; !existed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	turns := make([]ModelTurnTelemetry, 0, len(ids))
	for _, id := range ids {
		turns = append(turns, after[id])
	}
	return turns
}

func (h *DurableHarness) commit(expectedRevision uint64, checkpoint Checkpoint, events []Event) error {
	checkpointRecord, eventRecords, err := encodeCommit(checkpoint, events)
	if err != nil {
		return err
	}
	return h.store.CommitRuntime(checkpoint.RunID, expectedRevision, checkpointRecord, eventRecords)
}

func encodeCommit(checkpoint Checkpoint, events []Event) (store.RuntimeCheckpointRecord, []store.RuntimeEventRecord, error) {
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return store.RuntimeCheckpointRecord{}, nil, fmt.Errorf("encoding runtime checkpoint: %w", err)
	}
	records := make([]store.RuntimeEventRecord, len(events))
	for index, event := range events {
		eventPayload, err := json.Marshal(event)
		if err != nil {
			return store.RuntimeCheckpointRecord{}, nil, fmt.Errorf("encoding runtime event: %w", err)
		}
		records[index] = store.RuntimeEventRecord{
			RunID:         event.RunID,
			Revision:      event.Revision,
			EventType:     string(event.Type),
			OccurredAt:    event.OccurredAt,
			CorrelationID: event.CorrelationID,
			PayloadJSON:   string(eventPayload),
		}
	}
	return store.RuntimeCheckpointRecord{
		RunID:       checkpoint.RunID,
		Revision:    checkpoint.Revision,
		PayloadJSON: string(payload),
		UpdatedAt:   time.Now().UTC(),
	}, records, nil
}

func decodeCommit(checkpointRecord store.RuntimeCheckpointRecord, eventRecords []store.RuntimeEventRecord) (Checkpoint, []Event, error) {
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(checkpointRecord.PayloadJSON), &checkpoint); err != nil {
		return Checkpoint{}, nil, fmt.Errorf("decoding runtime checkpoint: %w", err)
	}
	if checkpoint.RunID != checkpointRecord.RunID || checkpoint.Revision != checkpointRecord.Revision {
		return Checkpoint{}, nil, fmt.Errorf("runtime checkpoint envelope does not match payload")
	}
	events := make([]Event, len(eventRecords))
	for index, record := range eventRecords {
		if err := json.Unmarshal([]byte(record.PayloadJSON), &events[index]); err != nil {
			return Checkpoint{}, nil, fmt.Errorf("decoding runtime event %d: %w", record.Sequence, err)
		}
		events[index].Sequence = record.Sequence
		if events[index].RunID != record.RunID || events[index].Revision != record.Revision || string(events[index].Type) != record.EventType {
			return Checkpoint{}, nil, fmt.Errorf("runtime event %d envelope does not match payload", record.Sequence)
		}
	}
	return checkpoint, events, nil
}
