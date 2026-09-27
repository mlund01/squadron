package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"squadron/aitools"
)

// TaskSession is one ephemeral, mission-owned executor session.
type TaskSession interface {
	Run(context.Context) (TaskResultEnvelope, error)
	Close() error
}

type ResumableTaskSession interface {
	TaskSession
	RunResumable(context.Context, TaskAttemptProgress, func(TaskAttemptProgress) error) (TaskResultEnvelope, error)
}

// TaskRecoveryBlockedError leaves the attempt active. It represents an
// operation whose external outcome is unknown and therefore must not enter the
// ordinary retry path.
type TaskRecoveryBlockedError struct {
	Err            error
	ToolUseID      string
	ToolName       string
	IdempotencyKey string
}

func (e *TaskRecoveryBlockedError) Error() string { return "task recovery blocked: " + e.Err.Error() }
func (e *TaskRecoveryBlockedError) Unwrap() error { return e.Err }

type MissionRecoveryWaitingError struct{ Blocks []RecoveryBlock }

func (e *MissionRecoveryWaitingError) Error() string {
	return fmt.Sprintf("mission is waiting for reconciliation of %d tool call(s)", len(e.Blocks))
}

// TaskHumanInputWaitingError is returned after a task tool has durably
// established a human request but before a model-visible tool result exists.
type TaskHumanInputWaitingError struct{ Wait HumanInputWait }

func (e *TaskHumanInputWaitingError) Error() string {
	return fmt.Sprintf("task %q is waiting for human input %q", e.Wait.TaskName, e.Wait.ToolCallID)
}

type MissionHumanInputWaitingError struct{ Waits []HumanInputWait }

func (e *MissionHumanInputWaitingError) Error() string {
	return fmt.Sprintf("mission is waiting for %d human input response(s)", len(e.Waits))
}

type MissionRetryWaitingError struct{ Deadlines map[string]time.Time }

func (e *MissionRetryWaitingError) Error() string {
	return fmt.Sprintf("mission is waiting for %d retry deadline(s)", len(e.Deadlines))
}

// TaskAttemptTimeoutError is a task failure, not a request to stop the run.
// In particular it must not wrap context.DeadlineExceeded, which callers use
// to classify cancellation of the entire mission.
type TaskAttemptTimeoutError struct {
	TaskName   string
	DeadlineAt time.Time
}

func (e *TaskAttemptTimeoutError) Error() string {
	return fmt.Sprintf("task %q exceeded its attempt deadline at %s", e.TaskName, e.DeadlineAt.Format(time.RFC3339Nano))
}

// TaskSessionFactory creates a fresh session from an immutable dispatch and
// its already-filtered capability map.
type TaskSessionFactory interface {
	NewTaskSession(context.Context, Dispatch, map[string]aitools.Tool) (TaskSession, error)
}

// ToolCallAuthorization is the complete non-secret context sent to the
// credential broker at the instant a granted tool is invoked.
type ToolCallAuthorization struct {
	RunID          string
	TaskName       string
	AttemptID      string
	RunAs          string
	ToolName       string
	ToolUseID      string
	IdempotencyKey string
	InputJSON      string
}

// CredentialBroker authorizes a tool call and resolves credential references
// only after authorization. Implementations must not return credentials to the
// model or persist them in the dispatch.
type CredentialBroker interface {
	AuthorizeToolInput(context.Context, ToolCallAuthorization) (string, error)
}

// BoundedExecutor materializes exactly the tools named by the persisted grant.
// It never shares a mutable tool map or session between attempts.
type BoundedExecutor struct {
	registry map[string]aitools.Tool
	factory  TaskSessionFactory
	broker   CredentialBroker
}

func NewBoundedExecutor(registry map[string]aitools.Tool, factory TaskSessionFactory, broker CredentialBroker) (*BoundedExecutor, error) {
	if factory == nil {
		return nil, fmt.Errorf("task session factory is required")
	}
	if broker == nil {
		return nil, fmt.Errorf("credential broker is required")
	}
	tools := make(map[string]aitools.Tool, len(registry))
	for name, tool := range registry {
		if tool == nil {
			return nil, fmt.Errorf("tool %q is nil", name)
		}
		tools[name] = tool
	}
	return &BoundedExecutor{registry: tools, factory: factory, broker: broker}, nil
}

func (e *BoundedExecutor) Execute(ctx context.Context, dispatch Dispatch) (TaskResultEnvelope, error) {
	session, err := e.newSession(ctx, dispatch)
	if err != nil {
		return TaskResultEnvelope{}, err
	}
	result, runErr := session.Run(ctx)
	closeErr := session.Close()
	if runErr != nil {
		return TaskResultEnvelope{}, runErr
	}
	if closeErr != nil {
		return TaskResultEnvelope{}, fmt.Errorf("close task session: %w", closeErr)
	}
	return result, nil
}

func (e *BoundedExecutor) ExecuteResumable(ctx context.Context, dispatch Dispatch, progress TaskAttemptProgress, commit func(TaskAttemptProgress) error) (TaskResultEnvelope, error) {
	session, err := e.newSession(ctx, dispatch)
	if err != nil {
		return TaskResultEnvelope{}, err
	}
	resumable, ok := session.(ResumableTaskSession)
	if !ok {
		_ = session.Close()
		return TaskResultEnvelope{}, &TaskRecoveryBlockedError{Err: fmt.Errorf("task session does not support durable resume")}
	}
	result, runErr := resumable.RunResumable(ctx, cloneTaskProgress(progress), commit)
	closeErr := session.Close()
	if runErr != nil {
		return TaskResultEnvelope{}, runErr
	}
	if closeErr != nil {
		return TaskResultEnvelope{}, fmt.Errorf("close task session: %w", closeErr)
	}
	return result, nil
}

func (e *BoundedExecutor) newSession(ctx context.Context, dispatch Dispatch) (TaskSession, error) {
	if err := validateExecutionGrant(dispatch.TaskName, dispatch.Grant); err != nil {
		return nil, err
	}
	tools := make(map[string]aitools.Tool, len(dispatch.Grant.Tools))
	for _, name := range dispatch.Grant.Tools {
		tool, ok := e.registry[name]
		if !ok {
			return nil, fmt.Errorf("granted tool %q is unavailable", name)
		}
		tools[name] = &authorizedTool{tool: tool, broker: e.broker, authorization: ToolCallAuthorization{
			RunID: dispatch.RunID, TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID,
			RunAs: dispatch.Grant.RunAs, ToolName: name,
		}}
	}
	session, err := e.factory.NewTaskSession(ctx, cloneDispatch(dispatch), tools)
	if err != nil {
		return nil, fmt.Errorf("create task session: %w", err)
	}
	if session == nil {
		return nil, fmt.Errorf("task session factory returned nil")
	}
	return session, nil
}

func (e *BoundedExecutor) Clarify(ctx context.Context, dispatch Dispatch, transcript []TaskSessionMessage, question string) (TaskClarificationResult, error) {
	clarifier, ok := e.factory.(interface {
		Clarify(context.Context, Dispatch, []TaskSessionMessage, string) (TaskClarificationResult, error)
	})
	if !ok {
		return TaskClarificationResult{}, fmt.Errorf("task session factory does not support clarification")
	}
	return clarifier.Clarify(ctx, cloneDispatch(dispatch), cloneTaskSessionMessages(transcript), question)
}

type authorizedTool struct {
	tool          aitools.Tool
	broker        CredentialBroker
	authorization ToolCallAuthorization
}

func (t *authorizedTool) ToolName() string        { return t.tool.ToolName() }
func (t *authorizedTool) ToolDescription() string { return t.tool.ToolDescription() }
func (t *authorizedTool) ToolReplaySafe() bool    { return aitools.IsReplaySafe(t.tool) }
func (t *authorizedTool) ToolPayloadSchema() aitools.Schema {
	return t.tool.ToolPayloadSchema()
}
func (t *authorizedTool) ToolOutputSchema() json.RawMessage {
	return aitools.ToolOutputSchemaOf(t.tool)
}
func (t *authorizedTool) Call(ctx context.Context, input string) string {
	request := t.authorization
	if metadata, ok := aitools.InvocationMetadataFromContext(ctx); ok {
		request.ToolUseID = metadata.ToolUseID
		request.IdempotencyKey = metadata.IdempotencyKey
	}
	request.InputJSON = input
	authorizedInput, err := t.broker.AuthorizeToolInput(ctx, request)
	if err != nil {
		return fmt.Sprintf("Error: tool authorization failed: %v", err)
	}
	return t.tool.Call(ctx, authorizedInput)
}

func (t *authorizedTool) CallDeferred(ctx context.Context, input string) (aitools.DeferredToolResult, bool) {
	deferred, ok := t.tool.(aitools.DeferredTool)
	if !ok {
		return aitools.DeferredToolResult{}, false
	}
	request := t.authorization
	if metadata, ok := aitools.InvocationMetadataFromContext(ctx); ok {
		request.ToolUseID = metadata.ToolUseID
		request.IdempotencyKey = metadata.IdempotencyKey
	}
	request.InputJSON = input
	authorizedInput, err := t.broker.AuthorizeToolInput(ctx, request)
	if err != nil {
		return aitools.DeferredToolResult{Result: fmt.Sprintf("Error: tool authorization failed: %v", err)}, true
	}
	return deferred.CallDeferred(ctx, authorizedInput)
}
