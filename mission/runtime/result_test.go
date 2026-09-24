package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"squadron/store"
)

func TestTaskResultRoundTripsRichContentArtifactsAndTypedData(t *testing.T) {
	t.Parallel()

	runtimeStore := store.NewMemoryRuntimeStore()
	harness, err := CreateDurableHarness(runtimeStore, "run-result", "commander-result", []TaskDefinition{{
		Name: "render",
		ResultPolicy: ResultPolicy{Fields: []ResultField{
			{Name: "title", Type: "string", Required: true},
			{Name: "pages", Type: "integer", Required: true},
		}},
	}})
	if err != nil {
		t.Fatalf("CreateDurableHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID: "render-1", TaskName: "render", ReadySetRevision: harness.Snapshot().Revision,
		Grant: testGrant("render"),
	})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	result := testResult(dispatch)
	result.Summary = "Rendered the report"
	result.Content = []store.MessagePart{
		{Type: "text", Text: "Rendered the report"},
		{Type: "document", DocumentData: "cGRm", DocumentMediaType: "application/pdf", DocumentFilename: "report.pdf"},
	}
	result.RawOutput = append([]store.MessagePart(nil), result.Content...)
	result.Artifacts = []ArtifactReference{{ID: "artifact-1", URI: "artifact://run-result/report", Name: "report.pdf", MediaType: "application/pdf"}}
	result.Data = json.RawMessage(`{"title":"Quarterly report","pages":2}`)

	if err := harness.CompleteTask("render", dispatch.AttemptID, result); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	restored, events, err := RestoreDurableHarness(runtimeStore, "run-result")
	if err != nil {
		t.Fatalf("RestoreDurableHarness() error = %v", err)
	}
	accepted, ok := restored.TaskResult("render")
	if !ok || !accepted.Validation.Valid || !reflect.DeepEqual(accepted.Envelope, result) {
		t.Fatalf("restored result = %#v, %v", accepted, ok)
	}
	inspection, err := restored.InspectTask(InspectTaskRequest{RequestID: "inspect-render", TaskName: "render", Revision: restored.Snapshot().Revision})
	if err != nil {
		t.Fatalf("InspectTask() error = %v", err)
	}
	if inspection.Summary != result.Summary || !reflect.DeepEqual(inspection.Content, result.Content) || string(inspection.Data) != string(result.Data) {
		t.Fatalf("inspection = %#v", inspection)
	}
	summary := restored.Snapshot().Results["render"]
	if summary.Summary != result.Summary || summary.ArtifactCount != 1 || !summary.HasData {
		t.Fatalf("bounded result summary = %#v", summary)
	}
	found := false
	for _, event := range events {
		if event.Type == EventTaskCompleted {
			found = event.Result != nil && reflect.DeepEqual(event.Result.Envelope, result)
		}
	}
	if !found {
		t.Fatal("completion event did not preserve the accepted result")
	}
}

func TestInvalidTypedResultCannotAdvanceDependencies(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-invalid", "commander-invalid", []TaskDefinition{
		{Name: "produce", ResultPolicy: ResultPolicy{Fields: []ResultField{{Name: "count", Type: "integer", Required: true}}}},
		{Name: "consume", DependsOn: []string{"produce"}},
	})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID: "produce-1", TaskName: "produce", ReadySetRevision: harness.Snapshot().Revision,
		Grant: testGrant("produce"),
	})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	result := testResult(dispatch)
	result.Data = json.RawMessage(`{"count":"many"}`)
	err = harness.CompleteTask("produce", dispatch.AttemptID, result)
	if err == nil || !strings.Contains(err.Error(), "data.count must be an integer") {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	snapshot := harness.Snapshot()
	if snapshot.Tasks["produce"].State != TaskRunning || snapshot.Tasks["consume"].State != TaskBlocked || len(snapshot.Ready) != 0 {
		t.Fatalf("invalid output advanced graph: %#v", snapshot)
	}
	if _, ok := harness.TaskResult("produce"); ok {
		t.Fatal("rejected result became visible")
	}
}

func TestNaturalLanguageResultRequiresNoTypedSchema(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-natural", "commander-natural", []TaskDefinition{{Name: "write"}})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID: "write-1", TaskName: "write", ReadySetRevision: harness.Snapshot().Revision,
		Grant: testGrant("write"),
	})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	result := testResult(dispatch)
	result.Summary = "A concise natural-language result"
	result.Content = []store.MessagePart{{Type: "text", Text: "A concise natural-language result"}}
	result.RawOutput = append([]store.MessagePart(nil), result.Content...)
	if err := harness.CompleteTask("write", dispatch.AttemptID, result); err != nil {
		t.Fatalf("CompleteTask() rejected unstructured content: %v", err)
	}
}
