package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CommanderDecision is one persisted commander turn and its requested
// intrinsic orchestration operation. Task tools are intentionally absent.
type CommanderDecision struct {
	Prompt   CommanderMessage
	Message  CommanderMessage
	Dispatch DispatchRequest
	Grant    GrantRequest
	Route    RouteRequest
	Inspect  InspectTaskRequest
	Clarify  ClarifyTaskRequest
}

// GrantRequest is the commander's bounded selection from declarative policy.
type GrantRequest struct {
	TaskName string
	Model    string
	Tools    []string
}

// GrantResolver validates the requested selection and returns the complete
// immutable grant. It never exposes a tool execution method to the commander.
type GrantResolver interface {
	ResolveExecutionGrant(GrantRequest) (ExecutionGrant, error)
}

// CommanderDriver is the one run-scoped model session that reasons over
// mission snapshots. An implementation retains its session for the entire run.
type CommanderDriver interface {
	Decide(context.Context, Snapshot, []CommanderMessage) (CommanderDecision, error)
	DispatchAccepted(Dispatch)
	RouteAccepted(RouteSelection)
	InspectionAccepted(TaskInspection)
}

// TaskExecutor performs one accepted attempt. Capability construction and
// result validation live behind this boundary and are implemented separately.
type TaskExecutor interface {
	Execute(context.Context, Dispatch) (TaskResultEnvelope, error)
}

type ResumableTaskExecutor interface {
	ExecuteResumable(context.Context, Dispatch, TaskAttemptProgress, func(TaskAttemptProgress) error) (TaskResultEnvelope, error)
}

// TaskClarifier restores a completed worker's persisted logical session and
// executes one no-tools follow-up turn.
type TaskClarifier interface {
	Clarify(context.Context, Dispatch, []TaskSessionMessage, string) (TaskClarificationResult, error)
}

// ExecutionObserver receives lifecycle notifications only after the matching
// durable transition is accepted. It is a projection hook, never a state
// authority.
type ExecutionObserver interface {
	TaskStarted(Dispatch)
	TaskCompleted(Dispatch)
	TaskFailed(Dispatch, error, bool)
	RouteSelected(RouteSelection)
}

// Coordinator drives a mission using one commander and the durable harness.
// It never creates a commander per task and cannot bypass graph eligibility.
type Coordinator struct {
	harness   *DurableHarness
	commander CommanderDriver
	executor  TaskExecutor
	grants    GrantResolver
	observer  ExecutionObserver
	ownerID   string
	leaseTTL  time.Duration
	heartbeat time.Duration
	now       func() time.Time
}

func (c *Coordinator) SetExecutionObserver(observer ExecutionObserver) {
	c.observer = observer
}

func NewCoordinator(harness *DurableHarness, commander CommanderDriver, executor TaskExecutor, grants GrantResolver) (*Coordinator, error) {
	if harness == nil {
		return nil, fmt.Errorf("durable harness is required")
	}
	if commander == nil {
		return nil, fmt.Errorf("commander driver is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("task executor is required")
	}
	if grants == nil {
		return nil, fmt.Errorf("execution grant resolver is required")
	}
	return &Coordinator{
		harness: harness, commander: commander, executor: executor, grants: grants,
		ownerID: uuid.NewString(), leaseTTL: 30 * time.Second, heartbeat: 10 * time.Second, now: time.Now,
	}, nil
}

// SetExecutionLeasePolicy configures coordinator ownership. It is primarily
// useful for hosts with a known worker identity and for deterministic tests.
func (c *Coordinator) SetExecutionLeasePolicy(ownerID string, ttl, heartbeat time.Duration) error {
	if ownerID == "" || ttl <= 0 || heartbeat <= 0 || heartbeat >= ttl {
		return fmt.Errorf("lease policy requires an owner and 0 < heartbeat < TTL")
	}
	c.ownerID, c.leaseTTL, c.heartbeat = ownerID, ttl, heartbeat
	return nil
}

// Run completes a mission while preserving single-commander ownership.
func (c *Coordinator) Run(ctx context.Context) error {
	snapshot := c.harness.Snapshot()
	switch snapshot.RunState {
	case RunPending:
		if err := c.harness.Start(); err != nil {
			return err
		}
	case RunStopped:
		if err := c.harness.Resume(); err != nil {
			return err
		}
	case RunRunning:
	case RunWaiting:
		return &MissionRecoveryWaitingError{Blocks: sortedRecoveryBlocks(snapshot.RecoveryBlocks)}
	case RunCompleted:
		return nil
	default:
		return fmt.Errorf("run cannot execute from %q", snapshot.RunState)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot = c.harness.Snapshot()
		if snapshot.AllComplete {
			return nil
		}
		if len(snapshot.PendingClarifications) > 0 {
			if err := c.resumeClarification(ctx, snapshot.PendingClarifications[0]); err != nil {
				return err
			}
			continue
		}
		if len(snapshot.PendingRoutes) > 0 {
			decision, err := c.commander.Decide(ctx, snapshot, c.harness.CommanderMessages())
			if err != nil {
				return fmt.Errorf("commander route decision: %w", err)
			}
			if decision.Inspect.TaskName != "" {
				inspection, err := c.harness.ApplyCommanderInspection(decision.Prompt, decision.Message, decision.Inspect)
				if err != nil {
					return fmt.Errorf("apply commander inspection: %w", err)
				}
				c.commander.InspectionAccepted(inspection)
				continue
			}
			if decision.Clarify.TaskName != "" {
				if err := c.harness.BeginCommanderClarification(decision.Prompt, decision.Message, decision.Clarify); err != nil {
					return fmt.Errorf("begin commander clarification: %w", err)
				}
				continue
			}
			selection, err := c.harness.ApplyCommanderRoute(decision.Prompt, decision.Message, decision.Route)
			if err != nil {
				return fmt.Errorf("apply commander route: %w", err)
			}
			c.commander.RouteAccepted(selection)
			if c.observer != nil {
				c.observer.RouteSelected(selection)
			}
			continue
		}
		for len(snapshot.Ready) > 0 && len(snapshot.Active) < snapshot.MaxParallel {
			decision, err := c.commander.Decide(ctx, snapshot, c.harness.CommanderMessages())
			if err != nil {
				return fmt.Errorf("commander decision: %w", err)
			}
			grant, err := c.grants.ResolveExecutionGrant(decision.Grant)
			if err != nil {
				return fmt.Errorf("resolve execution grant: %w", err)
			}
			decision.Dispatch.Grant = grant
			dispatch, err := c.harness.ApplyCommanderDispatch(decision.Prompt, decision.Message, decision.Dispatch)
			if err != nil {
				return fmt.Errorf("apply commander decision: %w", err)
			}
			c.commander.DispatchAccepted(dispatch)
			snapshot = c.harness.Snapshot()
		}
		if len(snapshot.Active) > 0 {
			if err := c.executeConcurrent(ctx, snapshot.Active); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("mission is running without an active or ready task")
	}
}

func (c *Coordinator) resumeClarification(ctx context.Context, pending ClarifyTaskRequest) error {
	clarifier, ok := c.executor.(TaskClarifier)
	if !ok {
		return fmt.Errorf("task executor does not support clarification")
	}
	request, dispatch, transcript, err := c.harness.ClarificationContext(pending.RequestID)
	if err != nil {
		return fmt.Errorf("load clarification context: %w", err)
	}
	result, err := clarifier.Clarify(ctx, dispatch, transcript, request.Question)
	if err != nil {
		return fmt.Errorf("clarify task %q: %w", request.TaskName, err)
	}
	clarification, err := c.harness.CompleteClarification(request.RequestID, request.MessageID, result)
	if err != nil {
		return fmt.Errorf("complete task clarification: %w", err)
	}
	if receiver, ok := c.commander.(interface{ ClarificationAccepted(TaskClarification) }); ok {
		receiver.ClarificationAccepted(clarification)
	}
	return nil
}

func (c *Coordinator) executeConcurrent(ctx context.Context, dispatches []Dispatch) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, dispatch := range dispatches {
		dispatch := dispatch
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.execute(ctx, dispatch); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	snapshot := c.harness.Snapshot()
	if len(snapshot.RecoveryBlocks) > 0 && snapshot.RunState == RunRunning {
		if err := c.harness.EnterRecoveryWaiting(); err != nil {
			return fmt.Errorf("persist recovery waiting state: %w", err)
		}
		snapshot = c.harness.Snapshot()
		return &MissionRecoveryWaitingError{Blocks: sortedRecoveryBlocks(snapshot.RecoveryBlocks)}
	}
	return firstErr
}

func sortedRecoveryBlocks(source map[string]RecoveryBlock) []RecoveryBlock {
	blocks := make([]RecoveryBlock, 0, len(source))
	for _, block := range source {
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].AttemptID < blocks[j].AttemptID })
	return blocks
}

func (c *Coordinator) execute(ctx context.Context, dispatch Dispatch) error {
	leaseToken := uuid.NewString()
	if err := c.acquireExecutionLease(ctx, dispatch.AttemptID, leaseToken); err != nil {
		return err
	}
	if c.observer != nil {
		c.observer.TaskStarted(dispatch)
	}
	execCtx, cancelExecution := context.WithCancel(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go c.renewExecutionLease(execCtx, dispatch.AttemptID, leaseToken, stopHeartbeat, heartbeatDone, cancelExecution)
	var result TaskResultEnvelope
	var err error
	if executor, ok := c.executor.(ResumableTaskExecutor); ok {
		progress, exists := c.harness.TaskProgress(dispatch.AttemptID)
		if !exists {
			close(stopHeartbeat)
			<-heartbeatDone
			cancelExecution()
			return fmt.Errorf("active attempt %q has no durable progress", dispatch.AttemptID)
		}
		result, err = executor.ExecuteResumable(execCtx, dispatch, progress, func(next TaskAttemptProgress) error {
			return c.harness.SaveTaskProgressWithLease(dispatch, next, leaseToken)
		})
	} else {
		result, err = c.executor.Execute(execCtx, dispatch)
	}
	close(stopHeartbeat)
	heartbeatErr := <-heartbeatDone
	cancelExecution()
	if heartbeatErr != nil {
		return heartbeatErr
	}
	if ctx.Err() != nil {
		if releaseErr := c.harness.ReleaseExecutionLease(dispatch.AttemptID, leaseToken); releaseErr != nil {
			var lost *ExecutionLeaseLostError
			if !errors.As(releaseErr, &lost) {
				return fmt.Errorf("release execution lease for task %q: %w", dispatch.TaskName, releaseErr)
			}
		}
		return ctx.Err()
	}
	if err != nil {
		var lost *ExecutionLeaseLostError
		if errors.As(err, &lost) {
			return lost
		}
		var blocked *TaskRecoveryBlockedError
		if errors.As(err, &blocked) {
			if blocked.ToolUseID == "" {
				return blocked
			}
			recovery := RecoveryBlock{
				AttemptID: dispatch.AttemptID, TaskName: dispatch.TaskName,
				ToolUseID: blocked.ToolUseID, ToolName: blocked.ToolName, IdempotencyKey: blocked.IdempotencyKey,
				Reason: blocked.Error(),
			}
			if persistErr := c.harness.BlockTaskRecoveryWithLease(dispatch, recovery, leaseToken); persistErr != nil {
				return fmt.Errorf("persist recovery block for task %q: %w", dispatch.TaskName, persistErr)
			}
			return &MissionRecoveryWaitingError{Blocks: []RecoveryBlock{recovery}}
		}
		retry, persistErr := c.harness.FailTaskWithLease(dispatch.TaskName, dispatch.AttemptID, err, leaseToken)
		if persistErr != nil {
			return fmt.Errorf("persist failure for task %q: %w", dispatch.TaskName, persistErr)
		}
		if c.observer != nil {
			c.observer.TaskFailed(dispatch, err, retry)
		}
		if retry {
			return nil
		}
		return fmt.Errorf("execute task %q: %w", dispatch.TaskName, err)
	}
	if err := c.harness.CompleteTaskWithLease(dispatch.TaskName, dispatch.AttemptID, result, leaseToken); err != nil {
		var rejected *ResultRejectedError
		if errors.As(err, &rejected) {
			retry, persistErr := c.harness.FailTaskWithLease(dispatch.TaskName, dispatch.AttemptID, rejected, leaseToken)
			if persistErr != nil {
				return fmt.Errorf("persist rejected result for task %q: %w", dispatch.TaskName, persistErr)
			}
			if c.observer != nil {
				c.observer.TaskFailed(dispatch, rejected, retry)
			}
			if retry {
				return nil
			}
		}
		return fmt.Errorf("complete task %q: %w", dispatch.TaskName, err)
	}
	if c.observer != nil {
		c.observer.TaskCompleted(dispatch)
	}
	return nil
}

func (c *Coordinator) acquireExecutionLease(ctx context.Context, attemptID, token string) error {
	for {
		_, err := c.harness.AcquireExecutionLease(attemptID, c.ownerID, token, c.now().UTC(), c.leaseTTL)
		if err == nil {
			return nil
		}
		var held *ExecutionLeaseHeldError
		if !errors.As(err, &held) {
			return fmt.Errorf("acquire execution lease for attempt %q: %w", attemptID, err)
		}
		wait := held.ExpiresAt.Sub(c.now().UTC())
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Coordinator) renewExecutionLease(ctx context.Context, attemptID, token string, stop <-chan struct{}, done chan<- error, cancel context.CancelFunc) {
	ticker := time.NewTicker(c.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			done <- nil
			return
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if _, err := c.harness.RenewExecutionLease(attemptID, token, c.now().UTC(), c.leaseTTL); err != nil {
				cancel()
				done <- fmt.Errorf("renew execution lease for attempt %q: %w", attemptID, err)
				return
			}
		}
	}
}
