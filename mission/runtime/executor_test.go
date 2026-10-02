package runtime

import (
	"context"
	"sync"
	"testing"

	"squadron/aitools"
	"squadron/store"
)

func TestBoundedExecutorCreatesIsolatedAttributedSessions(t *testing.T) {
	t.Parallel()

	factory := &recordingSessionFactory{}
	executor, err := NewBoundedExecutor(map[string]aitools.Tool{
		"search": stubTool{name: "search"},
		"files":  stubTool{name: "files"},
	}, factory, passthroughBroker{})
	if err != nil {
		t.Fatalf("NewBoundedExecutor() error = %v", err)
	}
	dispatches := []Dispatch{
		{RunID: "run-1", TaskName: "a", AttemptID: "run-1:a:1", Grant: ExecutionGrant{TaskName: "a", Model: "fast", Tools: []string{"search"}, RunAs: "user:one", ConfigurationVersion: "v1"}},
		{RunID: "run-1", TaskName: "b", AttemptID: "run-1:b:1", Grant: ExecutionGrant{TaskName: "b", Model: "strong", Tools: []string{"files"}, RunAs: "sp:worker", ConfigurationVersion: "v1"}},
	}
	var wg sync.WaitGroup
	for _, dispatch := range dispatches {
		wg.Add(1)
		go func(dispatch Dispatch) {
			defer wg.Done()
			if _, err := executor.Execute(context.Background(), dispatch); err != nil {
				t.Errorf("Execute(%s) error = %v", dispatch.TaskName, err)
			}
		}(dispatch)
	}
	wg.Wait()

	factory.mu.Lock()
	defer factory.mu.Unlock()
	if len(factory.calls) != 2 {
		t.Fatalf("session count = %d, want 2", len(factory.calls))
	}
	for _, call := range factory.calls {
		if len(call.tools) != 1 {
			t.Fatalf("task %s tools = %v", call.dispatch.TaskName, call.tools)
		}
		if call.dispatch.RunID != "run-1" || call.dispatch.Grant.RunAs == "" {
			t.Fatalf("missing attribution: %#v", call.dispatch)
		}
	}
	factory.calls[0].tools["sentinel"] = stubTool{name: "sentinel"}
	if _, shared := factory.calls[1].tools["sentinel"]; shared {
		t.Fatal("parallel sessions shared a mutable tool map")
	}
}

func TestBoundedExecutorRejectsUnavailableAndIntrinsicTools(t *testing.T) {
	t.Parallel()

	executor, err := NewBoundedExecutor(map[string]aitools.Tool{"search": stubTool{name: "search"}}, &recordingSessionFactory{}, passthroughBroker{})
	if err != nil {
		t.Fatalf("NewBoundedExecutor() error = %v", err)
	}
	base := Dispatch{RunID: "run-1", TaskName: "work", Grant: ExecutionGrant{
		TaskName: "work", Model: "fast", RunAs: "user:one", ConfigurationVersion: "v1",
	}}
	base.Grant.Tools = []string{"missing"}
	if _, err := executor.Execute(context.Background(), base); err == nil {
		t.Fatal("Execute() accepted an unavailable capability")
	}
	base.Grant.Tools = []string{"start_task"}
	if _, err := executor.Execute(context.Background(), base); err == nil {
		t.Fatal("Execute() accepted a commander control")
	}
}

func TestBoundedExecutorExposesOnlyGrantedAttemptTools(t *testing.T) {
	t.Parallel()
	factory := &recordingSessionFactory{}
	executor, err := NewBoundedExecutor(nil, factory, passthroughBroker{})
	if err != nil {
		t.Fatal(err)
	}
	executor.SetAttemptToolFactory(func(_ context.Context, dispatch Dispatch) (map[string]aitools.Tool, error) {
		if dispatch.AttemptID != "run-1:work:1" {
			t.Fatalf("attempt factory received wrong dispatch: %#v", dispatch)
		}
		return map[string]aitools.Tool{"file_read": stubTool{name: "file_read"}, "file_create": stubTool{name: "file_create"}}, nil
	})
	dispatch := Dispatch{RunID: "run-1", TaskName: "work", AttemptID: "run-1:work:1", Grant: ExecutionGrant{
		TaskName: "work", Model: "fast", Tools: []string{"file_read"}, RunAs: "user:one", ConfigurationVersion: "v1",
	}}
	if _, err := executor.Execute(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if len(factory.calls) != 1 || len(factory.calls[0].tools) != 1 || factory.calls[0].tools["file_read"] == nil || factory.calls[0].tools["file_create"] != nil {
		t.Fatalf("attempt tools escaped durable grant: %#v", factory.calls)
	}
}

func TestBoundedExecutorInjectsCredentialsOnlyAtToolCall(t *testing.T) {
	t.Parallel()

	tool := &capturingTool{name: "search"}
	broker := &recordingBroker{authorizedInput: `{"token":"resolved"}`}
	factory := &recordingSessionFactory{}
	executor, err := NewBoundedExecutor(map[string]aitools.Tool{"search": tool}, factory, broker)
	if err != nil {
		t.Fatalf("NewBoundedExecutor() error = %v", err)
	}
	dispatch := Dispatch{
		RunID: "run-1", TaskName: "collect", AttemptID: "run-1:collect:1",
		Grant: ExecutionGrant{TaskName: "collect", Model: "fast", Tools: []string{"search"}, RunAs: "sp:crawler", ConfigurationVersion: "v1"},
	}
	if _, err := executor.Execute(context.Background(), dispatch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	factory.mu.Lock()
	wrapped := factory.calls[0].tools["search"]
	factory.mu.Unlock()
	metadata := aitools.InvocationMetadata{RunID: "run-1", TaskName: "collect", AttemptID: "run-1:collect:1", ToolUseID: "call-1", IdempotencyKey: "key-1"}
	result := wrapped.Call(aitools.WithInvocationMetadata(context.Background(), metadata), `{"token":"${secrets.api_key}"}`)
	if result != "ok" || tool.input != broker.authorizedInput {
		t.Fatalf("tool result/input = %q/%q", result, tool.input)
	}
	if broker.request.RunID != "run-1" || broker.request.TaskName != "collect" || broker.request.AttemptID != "run-1:collect:1" || broker.request.RunAs != "sp:crawler" {
		t.Fatalf("broker attribution = %#v", broker.request)
	}
	if broker.request.InputJSON != `{"token":"${secrets.api_key}"}` {
		t.Fatalf("broker raw input = %q", broker.request.InputJSON)
	}
	if broker.request.ToolUseID != "call-1" || broker.request.IdempotencyKey != "key-1" {
		t.Fatalf("broker invocation identity = %#v", broker.request)
	}
	if tool.metadata != metadata {
		t.Fatalf("wrapped tool metadata = %#v, want %#v", tool.metadata, metadata)
	}
}

type recordedSessionCall struct {
	dispatch Dispatch
	tools    map[string]aitools.Tool
}

type recordingSessionFactory struct {
	mu    sync.Mutex
	calls []recordedSessionCall
}

func (f *recordingSessionFactory) NewTaskSession(_ context.Context, dispatch Dispatch, tools map[string]aitools.Tool) (TaskSession, error) {
	f.mu.Lock()
	f.calls = append(f.calls, recordedSessionCall{dispatch: dispatch, tools: tools})
	f.mu.Unlock()
	return &stubTaskSession{}, nil
}

type stubTaskSession struct{}

func (*stubTaskSession) Run(context.Context) (TaskResultEnvelope, error) {
	return TaskResultEnvelope{Status: ResultSucceeded, Summary: "done", RawOutput: []store.MessagePart{{Type: "text", Text: "done"}}}, nil
}
func (*stubTaskSession) Close() error { return nil }

type stubTool struct{ name string }

func (t stubTool) ToolName() string                  { return t.name }
func (t stubTool) ToolDescription() string           { return t.name }
func (stubTool) ToolPayloadSchema() aitools.Schema   { return aitools.Schema{Type: aitools.TypeObject} }
func (stubTool) Call(context.Context, string) string { return "ok" }

type passthroughBroker struct{}

func (passthroughBroker) AuthorizeToolInput(_ context.Context, request ToolCallAuthorization) (string, error) {
	return request.InputJSON, nil
}

type recordingBroker struct {
	request         ToolCallAuthorization
	authorizedInput string
}

func (b *recordingBroker) AuthorizeToolInput(_ context.Context, request ToolCallAuthorization) (string, error) {
	b.request = request
	return b.authorizedInput, nil
}

type capturingTool struct {
	name     string
	input    string
	metadata aitools.InvocationMetadata
}

func (t *capturingTool) ToolName() string        { return t.name }
func (t *capturingTool) ToolDescription() string { return t.name }
func (*capturingTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{Type: aitools.TypeObject}
}
func (t *capturingTool) Call(ctx context.Context, input string) string {
	t.input = input
	t.metadata, _ = aitools.InvocationMetadataFromContext(ctx)
	return "ok"
}
