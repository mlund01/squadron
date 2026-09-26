package runtime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"squadron/store"
)

func TestLinearGraphCompletesWithOneCommanderSession(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-1", "commander-1", []TaskDefinition{
		{Name: "collect"},
		{Name: "analyze", DependsOn: []string{"collect"}},
		{Name: "publish", DependsOn: []string{"analyze"}},
	})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for _, taskName := range []string{"collect", "analyze", "publish"} {
		snapshot := harness.Snapshot()
		if snapshot.Commander.ID != "commander-1" || snapshot.Commander.State != CommanderActive {
			t.Fatalf("commander changed during run: %#v", snapshot.Commander)
		}
		if len(snapshot.Ready) != 1 || snapshot.Ready[0] != taskName {
			t.Fatalf("ready set = %v, want [%s]", snapshot.Ready, taskName)
		}
		dispatch, err := harness.DispatchTask(DispatchRequest{
			RequestID:        "dispatch-" + taskName,
			TaskName:         taskName,
			ReadySetRevision: snapshot.Revision,
			Grant:            testGrant(taskName),
		})
		if err != nil {
			t.Fatalf("DispatchTask(%s) error = %v", taskName, err)
		}
		if dispatch.CommanderID != "commander-1" {
			t.Fatalf("dispatch commander = %q", dispatch.CommanderID)
		}
		if err := harness.CompleteTask(taskName, dispatch.AttemptID, testResult(dispatch)); err != nil {
			t.Fatalf("CompleteTask(%s) error = %v", taskName, err)
		}
	}

	final := harness.Snapshot()
	if final.RunState != RunCompleted || final.Commander.State != CommanderCompleted || !final.AllComplete {
		t.Fatalf("unexpected final snapshot: %#v", final)
	}
}

func testResult(dispatch Dispatch) TaskResultEnvelope {
	return TaskResultEnvelope{
		Status: ResultSucceeded, Summary: "done",
		Content:   []store.MessagePart{{Type: "text", Text: "done"}},
		RawOutput: []store.MessagePart{{Type: "text", Text: "done"}},
		Provenance: ResultProvenance{
			RunID: dispatch.RunID, TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID,
			Model: dispatch.Grant.Model, RunAs: dispatch.Grant.RunAs,
			ConfigurationVersion: dispatch.Grant.ConfigurationVersion,
		},
	}
}

func TestDispatchRejectsIneligibleAndStaleDecisions(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-1", "commander-1", []TaskDefinition{
		{Name: "first"},
		{Name: "second", DependsOn: []string{"first"}},
	})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()

	_, err = harness.DispatchTask(DispatchRequest{
		RequestID:        "bad-dependency",
		TaskName:         "second",
		ReadySetRevision: snapshot.Revision,
		Grant:            testGrant("second"),
	})
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("ineligible dispatch error = %v", err)
	}

	dispatch, err := harness.DispatchTask(DispatchRequest{
		RequestID:        "first-dispatch",
		TaskName:         "first",
		ReadySetRevision: snapshot.Revision,
		Grant:            testGrant("first"),
	})
	if err != nil {
		t.Fatalf("eligible dispatch error = %v", err)
	}
	_, err = harness.DispatchTask(DispatchRequest{
		RequestID:        "stale-dispatch",
		TaskName:         "second",
		ReadySetRevision: snapshot.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "stale ready-set revision") {
		t.Fatalf("stale dispatch error = %v", err)
	}

	repeated, err := harness.DispatchTask(DispatchRequest{
		RequestID:        "first-dispatch",
		TaskName:         "first",
		ReadySetRevision: snapshot.Revision,
	})
	if err != nil || !reflect.DeepEqual(repeated, dispatch) {
		t.Fatalf("idempotent dispatch = %#v, %v; want %#v", repeated, err, dispatch)
	}
}

func TestHarnessEnforcesParallelCeilingAndRejectsStaleCompletion(t *testing.T) {
	t.Parallel()

	harness, err := NewHarnessWithMaxParallel("run-parallel", "commander-parallel", []TaskDefinition{{Name: "a"}, {Name: "b"}}, 1)
	if err != nil {
		t.Fatalf("NewHarnessWithMaxParallel() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "a-1", TaskName: "a", ReadySetRevision: snapshot.Revision, Grant: testGrant("a")})
	if err != nil {
		t.Fatalf("DispatchTask(a) error = %v", err)
	}
	snapshot = harness.Snapshot()
	_, err = harness.DispatchTask(DispatchRequest{RequestID: "b-1", TaskName: "b", ReadySetRevision: snapshot.Revision, Grant: testGrant("b")})
	if err == nil || !strings.Contains(err.Error(), "maximum of 1") {
		t.Fatalf("parallel ceiling error = %v", err)
	}
	if err := harness.CompleteTask("a", "stale-attempt", testResult(dispatch)); err == nil {
		t.Fatal("stale completion was accepted")
	}
	if state := harness.Snapshot().Tasks["a"].State; state != TaskRunning {
		t.Fatalf("task state after stale completion = %q", state)
	}
}

func TestHarnessRejectsStaleFailureWithoutChangingActiveAttempt(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-stale-failure", "commander", []TaskDefinition{{Name: "work", MaxRetries: 1}})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "work-1", TaskName: "work", ReadySetRevision: snapshot.Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if _, err := harness.FailTask("work", "old-attempt", fmt.Errorf("late failure")); err == nil {
		t.Fatal("stale failure was accepted")
	}
	snapshot = harness.Snapshot()
	if snapshot.Tasks["work"].State != TaskRunning || len(snapshot.Active) != 1 || snapshot.Active[0].AttemptID != dispatch.AttemptID {
		t.Fatalf("active attempt changed after stale failure: %#v", snapshot)
	}
}

func TestFailedAttemptRetainsTranscriptForInspectionAcrossRetry(t *testing.T) {
	t.Parallel()
	harness, err := NewHarness("run-failed-transcript", "commander", []TaskDefinition{{Name: "work", MaxRetries: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	first, err := harness.DispatchTask(DispatchRequest{RequestID: "work-1", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatal(err)
	}
	progress := TaskAttemptProgress{AttemptID: first.AttemptID, Phase: "model_turn", Transcript: []TaskSessionMessage{{Role: "assistant", Parts: []store.MessagePart{{Type: "text", Text: "Partial work"}}}}}
	if err := harness.SaveTaskProgress(first, progress); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.FailTask("work", first.AttemptID, fmt.Errorf("provider disconnected")); err != nil {
		t.Fatal(err)
	}
	retained := harness.Checkpoint().TaskProgress[first.AttemptID]
	if retained.Phase != "failed" || len(retained.Transcript) != 1 || retained.Transcript[0].Parts[0].Text != "Partial work" {
		t.Fatalf("failed attempt transcript = %#v", retained)
	}
	second, err := harness.DispatchTask(DispatchRequest{RequestID: "work-2", TaskName: "work", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("work")})
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID || harness.Checkpoint().TaskProgress[first.AttemptID].Phase != "failed" {
		t.Fatal("retry replaced the failed attempt's transcript")
	}
}

func TestHarnessSelectsDeclaredRouteAndIgnoresUnchosenBranch(t *testing.T) {
	t.Parallel()

	harness, err := NewHarness("run-route", "commander-route", []TaskDefinition{
		{Name: "classify", Routes: []RouteDefinition{{Target: "billing", Condition: "billing"}, {Target: "support", Condition: "support"}}},
		{Name: "billing"}, {Name: "support"},
	})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	if !reflect.DeepEqual(snapshot.Ready, []string{"classify"}) {
		t.Fatalf("initial ready = %v", snapshot.Ready)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "classify-1", TaskName: "classify", ReadySetRevision: snapshot.Revision, Grant: testGrant("classify")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if err := harness.CompleteTask("classify", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	snapshot = harness.Snapshot()
	if len(snapshot.PendingRoutes["classify"]) != 2 || len(snapshot.Ready) != 0 || snapshot.AllComplete {
		t.Fatalf("pending route snapshot = %#v", snapshot)
	}
	if _, err := harness.SelectRoute(RouteRequest{RequestID: "bad-route", TaskName: "classify", Target: "unknown", Revision: snapshot.Revision}); err == nil {
		t.Fatal("undeclared route was accepted")
	}
	selection, err := harness.SelectRoute(RouteRequest{RequestID: "route-1", TaskName: "classify", Target: "billing", Revision: snapshot.Revision})
	if err != nil {
		t.Fatalf("SelectRoute() error = %v", err)
	}
	if selection.Target != "billing" {
		t.Fatalf("selection = %#v", selection)
	}
	snapshot = harness.Snapshot()
	if !reflect.DeepEqual(snapshot.Ready, []string{"billing"}) || snapshot.Tasks["support"].State != TaskBlocked {
		t.Fatalf("selected branch snapshot = %#v", snapshot)
	}
	dispatch, err = harness.DispatchTask(DispatchRequest{RequestID: "billing-1", TaskName: "billing", ReadySetRevision: snapshot.Revision, Grant: testGrant("billing")})
	if err != nil {
		t.Fatalf("DispatchTask(billing) error = %v", err)
	}
	if err := harness.CompleteTask("billing", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask(billing) error = %v", err)
	}
	if snapshot = harness.Snapshot(); snapshot.RunState != RunCompleted || !snapshot.AllComplete {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
}

func TestHarnessPersistsCrossMissionRouteAsCompletedLaunchIntent(t *testing.T) {
	harness, err := NewHarness("run-cross-mission", "commander-cross-mission", []TaskDefinition{{
		Name: "classify", Routes: []RouteDefinition{{
			Target: "follow_up", Condition: "needs follow-up", IsMission: true,
			Inputs: []RouteInputDefinition{{Name: "topic", Type: "string", Required: true}},
		}},
	}})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "classify-1", TaskName: "classify", ReadySetRevision: snapshot.Revision, Grant: testGrant("classify")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if err := harness.CompleteTask("classify", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	snapshot = harness.Snapshot()
	selection, err := harness.SelectRoute(RouteRequest{
		RequestID: "route-1", TaskName: "classify", Target: "follow_up",
		Inputs: map[string]string{"topic": "durability"}, Revision: snapshot.Revision,
	})
	if err != nil {
		t.Fatalf("SelectRoute() error = %v", err)
	}
	if !selection.IsMission || selection.Inputs["topic"] != "durability" {
		t.Fatalf("cross-mission selection = %#v", selection)
	}
	if snapshot = harness.Snapshot(); snapshot.RunState != RunCompleted || !snapshot.AllComplete {
		t.Fatalf("cross-mission snapshot = %#v", snapshot)
	}
	launch := snapshot.CrossMissionLaunches["route-1"]
	if launch.State != CrossMissionLaunchPending || launch.Target != "follow_up" || launch.Inputs["topic"] != "durability" {
		t.Fatalf("pending cross-mission launch = %#v", launch)
	}
	launch, err = harness.CompleteCrossMissionLaunch("route-1", "child-run-1")
	if err != nil {
		t.Fatalf("CompleteCrossMissionLaunch() error = %v", err)
	}
	if launch.State != CrossMissionLaunchLaunched || launch.ChildRunID != "child-run-1" {
		t.Fatalf("completed cross-mission launch = %#v", launch)
	}
	if _, err := harness.CompleteCrossMissionLaunch("route-1", "child-run-1"); err != nil {
		t.Fatalf("idempotent CompleteCrossMissionLaunch() error = %v", err)
	}
	if _, err := harness.CompleteCrossMissionLaunch("route-1", "different-child"); err == nil {
		t.Fatal("CompleteCrossMissionLaunch() accepted a second child run")
	}
	restored, err := RestoreHarness(harness.Checkpoint())
	if err != nil {
		t.Fatalf("RestoreHarness() error = %v", err)
	}
	selection = restored.Snapshot().RouteSelections["classify"]
	if !selection.IsMission || selection.Target != "follow_up" || selection.Inputs["topic"] != "durability" {
		t.Fatalf("restored cross-mission selection = %#v", selection)
	}
	launch = restored.Snapshot().CrossMissionLaunches["route-1"]
	if launch.State != CrossMissionLaunchLaunched || launch.ChildRunID != "child-run-1" {
		t.Fatalf("restored cross-mission launch = %#v", launch)
	}
}

func TestHarnessRejectsInvalidCanonicalCrossMissionInputs(t *testing.T) {
	t.Parallel()
	harness, err := NewHarness("run-route-inputs", "commander-route-inputs", []TaskDefinition{{
		Name: "classify", Routes: []RouteDefinition{{
			Target: "follow_up", Condition: "needs follow-up", IsMission: true,
			Inputs: []RouteInputDefinition{{Name: "enabled", Type: "bool", Required: true}},
		}},
	}})
	if err != nil {
		t.Fatalf("NewHarness() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "dispatch", TaskName: "classify", ReadySetRevision: harness.Snapshot().Revision, Grant: testGrant("classify")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if err := harness.CompleteTask("classify", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	_, err = harness.SelectRoute(RouteRequest{
		RequestID: "route", TaskName: "classify", Target: "follow_up",
		Inputs: map[string]string{"enabled": "not-a-bool", "extra": "value"}, Revision: harness.Snapshot().Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "inputs.enabled: expected bool") || !strings.Contains(err.Error(), "inputs.extra is not declared") {
		t.Fatalf("SelectRoute() error = %v", err)
	}
	if len(harness.Snapshot().CrossMissionLaunches) != 0 {
		t.Fatalf("invalid route created launch = %#v", harness.Snapshot().CrossMissionLaunches)
	}
}

func TestHarnessSendToActivatesAllTargets(t *testing.T) {
	t.Parallel()
	harness, err := NewHarnessWithMaxParallel("run-send", "commander-send", []TaskDefinition{
		{Name: "source", SendTo: []string{"left", "right"}}, {Name: "left"}, {Name: "right"},
	}, 2)
	if err != nil {
		t.Fatalf("NewHarnessWithMaxParallel() error = %v", err)
	}
	if err := harness.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshot := harness.Snapshot()
	if !reflect.DeepEqual(snapshot.Ready, []string{"source"}) {
		t.Fatalf("initial ready = %v", snapshot.Ready)
	}
	dispatch, err := harness.DispatchTask(DispatchRequest{RequestID: "source-1", TaskName: "source", ReadySetRevision: snapshot.Revision, Grant: testGrant("source")})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if err := harness.CompleteTask("source", dispatch.AttemptID, testResult(dispatch)); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	if snapshot = harness.Snapshot(); !reflect.DeepEqual(snapshot.Ready, []string{"left", "right"}) {
		t.Fatalf("send_to ready = %v", snapshot.Ready)
	}
}

func testGrant(taskName string) ExecutionGrant {
	return ExecutionGrant{TaskName: taskName, Model: "test-model", RunAs: "user:test", ConfigurationVersion: "test-config"}
}

func TestHarnessRejectsInvalidGraph(t *testing.T) {
	t.Parallel()

	_, err := NewHarness("run-1", "commander-1", []TaskDefinition{
		{Name: "a", DependsOn: []string{"b"}},
		{Name: "b", DependsOn: []string{"a"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}
