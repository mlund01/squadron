package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"squadron/aitools"
	"squadron/llm"
	"squadron/store"
)

func testMemoryGrant(access string) *TaskMemoryGrant {
	return &TaskMemoryGrant{
		MissionName: "research", Access: access, RetentionDays: 30,
		Fields: []ResultField{{Name: "summary", Type: "string"}},
	}
}

func TestMemoryProposalToolRecordsNoDirectWrite(t *testing.T) {
	grant := testMemoryGrant("read_write")
	tool := &memoryProposalTool{grant: *grant}
	if got := tool.Call(context.Background(), `{"key":"summary","value":"saved","expected_version":0}`); got != `{"status":"accepted"}` {
		t.Fatalf("proposal response = %q", got)
	}
	for _, input := range []string{
		`{"key":"unknown","value":"saved","expected_version":0}`,
		`{"key":"summary","value":17,"expected_version":0}`,
		`{"key":"summary","value":"saved","expected_version":1}`,
	} {
		got := tool.Call(context.Background(), input)
		var response struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(got), &response); err != nil || response.Status != "error" {
			t.Fatalf("invalid proposal %s accepted: %s (err %v)", input, got, err)
		}
	}
	if got := (&memoryProposalTool{grant: *testMemoryGrant("read")}).Call(context.Background(), `{"key":"summary","value":"saved","expected_version":0}`); got == `{"status":"accepted"}` {
		t.Fatal("read-only grant accepted write proposal")
	}
}

func TestModelTaskSessionReturnsRecordedMemoryProposals(t *testing.T) {
	model := &fakeTaskModelSession{responses: []*llm.ChatResponse{
		{ContentBlocks: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
			ID: "memory-1", Name: memoryProposalToolName,
			Input: json.RawMessage(`{"key":"summary","value":"saved","expected_version":0}`),
		}}}},
		{Content: "done"},
	}}
	factory, err := NewModelTaskSessionFactory(&fakeTaskModelSessionBuilder{session: model}, []TaskObjective{{Name: "work", Objective: "Record a finding"}})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := Dispatch{RunID: "run-1", TaskName: "work", AttemptID: "run-1:work:1", Grant: ExecutionGrant{
		TaskName: "work", Model: "fast", InputsJSON: `{}`, Memory: testMemoryGrant("read_write"),
	}}
	session, err := factory.NewTaskSession(context.Background(), dispatch, map[string]aitools.Tool{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MemoryProposals) != 1 || result.MemoryProposals[0].Key != "summary" || string(result.MemoryProposals[0].Value) != `"saved"` {
		t.Fatalf("memory proposals = %+v", result.MemoryProposals)
	}
}

func TestDurableMemoryProposalCommitsWithTaskAndConflictsAtomically(t *testing.T) {
	memoryStore := store.NewMemoryRuntimeStore()
	makeRun := func(runID string) (*DurableHarness, Dispatch) {
		harness, err := CreateDurableHarness(memoryStore, runID, runID+":commander", []TaskDefinition{{Name: "work", MemoryPolicy: &TaskMemoryPolicy{
			MissionName: "research", Access: "read_write", RetentionDays: 30,
			Fields: []ResultField{{Name: "summary", Type: "string"}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := harness.Start(); err != nil {
			t.Fatal(err)
		}
		grant := testGrant("work")
		grant.Memory = testMemoryGrant("read_write")
		dispatch, err := harness.DispatchTask(DispatchRequest{
			RequestID: runID + ":start", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: grant,
		})
		if err != nil {
			t.Fatal(err)
		}
		return harness, dispatch
	}
	first, firstDispatch := makeRun("run-1")
	second, secondDispatch := makeRun("run-2")
	result := testResult(firstDispatch)
	result.MemoryProposals = []MemoryProposal{{Key: "summary", Value: json.RawMessage(`"saved"`), ExpectedVersion: 0}}
	if err := first.CompleteTask("work", firstDispatch.AttemptID, result); err != nil {
		t.Fatal(err)
	}
	restored, events, err := RestoreDurableHarness(memoryStore, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.TaskResult("work"); !ok {
		t.Fatal("task result did not survive restart")
	}
	foundAudit := false
	for _, event := range events {
		if event.Type == EventMemoryUpdateCommitted && event.MemoryUpdate != nil && event.MemoryUpdate.Version == 1 {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatal("memory commit has no runtime audit event")
	}
	entries, err := memoryStore.LoadMissionMemory("research")
	if err != nil || len(entries) != 1 || entries[0].Version != 1 {
		t.Fatalf("memory after commit = %+v err=%v", entries, err)
	}
	conflicting := testResult(secondDispatch)
	conflicting.MemoryProposals = []MemoryProposal{{Key: "summary", Value: json.RawMessage(`"stale"`), ExpectedVersion: 0}}
	err = second.CompleteTask("work", secondDispatch.AttemptID, conflicting)
	var rejected *ResultRejectedError
	if !errors.As(err, &rejected) || !errors.Is(err, store.ErrMissionMemoryConflict) {
		t.Fatalf("stale proposal error = %v", err)
	}
	if _, ok := second.TaskResult("work"); ok {
		t.Fatal("conflicting task result was accepted")
	}
	checkpoint, _, err := memoryStore.LoadRuntime("run-2")
	if err != nil || checkpoint.Revision != second.Snapshot().Revision {
		t.Fatalf("conflicting checkpoint advanced: %+v err=%v", checkpoint, err)
	}
}

func TestTaskMemoryPolicyRejectsForgedGrants(t *testing.T) {
	policy := &TaskMemoryPolicy{MissionName: "research", Access: "read", RetentionDays: 30,
		Fields: []ResultField{{Name: "summary", Type: "string"}}}
	harness, err := NewHarness("run-policy", "commander", []TaskDefinition{{Name: "work", MemoryPolicy: policy}, {Name: "no_memory"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	grant := testGrant("work")
	grant.Memory = testMemoryGrant("read_write")
	request := DispatchRequest{RequestID: "forged", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: grant}
	if _, err := harness.DispatchTask(request); err == nil {
		t.Fatal("read-only task accepted a write grant")
	}
	request.Grant.Memory.Access = "read"
	request.Grant.Memory.Fields = append(request.Grant.Memory.Fields, ResultField{Name: "extra", Type: "string"})
	if _, err := harness.DispatchTask(request); err == nil {
		t.Fatal("task accepted an undeclared memory field")
	}
	request.Grant.Memory.Fields = request.Grant.Memory.Fields[:1]
	if _, err := harness.DispatchTask(request); err != nil {
		t.Fatalf("valid read grant rejected: %v", err)
	}
	noMemory := testGrant("no_memory")
	noMemory.Memory = testMemoryGrant("read")
	if _, err := harness.DispatchTask(DispatchRequest{RequestID: "forged-other", TaskName: "no_memory", ReadySetRevision: harness.Snapshot().Revision, Grant: noMemory}); err == nil {
		t.Fatal("ungranted task accepted memory snapshot")
	}
}
