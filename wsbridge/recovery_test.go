package wsbridge_test

import (
	"testing"

	"squadron/config"
	missionruntime "squadron/mission/runtime"
	"squadron/store"
	"squadron/wsbridge"
)

func TestResumeOrphanedMissionsAcknowledgesExistingCrossMissionChild(t *testing.T) {
	bundle := newTestBundle(t)
	parentID, err := bundle.Missions.CreateMission("parent", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateMission(parent) error = %v", err)
	}
	parent, err := missionruntime.CreateDurableHarness(bundle.Runtime, parentID, parentID+":commander", []missionruntime.TaskDefinition{{
		Name: "classify", Routes: []missionruntime.RouteDefinition{{
			Target: "child", Condition: "needs child", IsMission: true,
			Inputs: []missionruntime.RouteInputDefinition{{Name: "topic", Type: "string", Required: true}},
		}},
	}})
	if err != nil {
		t.Fatalf("CreateDurableHarness(parent) error = %v", err)
	}
	if err := parent.Start(); err != nil {
		t.Fatalf("Start(parent) error = %v", err)
	}
	grant := missionruntime.ExecutionGrant{
		TaskName: "classify", Model: "fast", RunAs: "user:test", InputsJSON: `{}`, PolicyJSON: `{}`, ConfigurationVersion: "v1",
	}
	dispatch, err := parent.DispatchTask(missionruntime.DispatchRequest{
		RequestID: "dispatch-1", TaskName: "classify", ReadySetRevision: parent.Snapshot().Revision, Grant: grant,
	})
	if err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	result := missionruntime.TaskResultEnvelope{
		Status: missionruntime.ResultSucceeded, Summary: "Needs child",
		RawOutput: []store.MessagePart{{Type: "text", Text: "Needs child"}},
		Provenance: missionruntime.ResultProvenance{
			RunID: parentID, TaskName: "classify", AttemptID: dispatch.AttemptID,
			Model: "fast", RunAs: "user:test", ConfigurationVersion: "v1",
		},
	}
	if err := parent.CompleteTask("classify", dispatch.AttemptID, result); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	routeRequest := missionruntime.RouteRequest{
		RequestID: "route-1", TaskName: "classify", Target: "child",
		Inputs: map[string]string{"topic": "durability"}, Revision: parent.Snapshot().Revision,
	}
	_, err = parent.ApplyCommanderRoute(
		missionruntime.CommanderMessage{ID: "route-prompt", Role: "user", Parts: []store.MessagePart{{Type: "text", Text: "choose route"}}},
		missionruntime.CommanderMessage{ID: "route-response", Role: "assistant", Parts: []store.MessagePart{{Type: "tool_use", ToolUseID: "route-1", ToolName: "select_route"}}},
		routeRequest,
	)
	if err != nil {
		t.Fatalf("SelectRoute() error = %v", err)
	}
	if err := bundle.Missions.UpdateMissionStatus(parentID, "completed"); err != nil {
		t.Fatalf("UpdateMissionStatus(parent) error = %v", err)
	}

	childID, err := bundle.Missions.CreateMissionForLaunch(parentID, "route-1", "child", `{"topic":"durability"}`, `{}`)
	if err != nil {
		t.Fatalf("CreateMissionForLaunch() error = %v", err)
	}
	if _, err := missionruntime.CreateDurableHarness(bundle.Runtime, childID, childID+":commander", []missionruntime.TaskDefinition{{Name: "work"}}); err != nil {
		t.Fatalf("CreateDurableHarness(child) error = %v", err)
	}
	if err := bundle.Missions.UpdateMissionStatus(childID, "completed"); err != nil {
		t.Fatalf("UpdateMissionStatus(child) error = %v", err)
	}

	client := wsbridge.NewClient(&config.Config{}, true, "", "", bundle, "test")
	client.ResumeOrphanedMissions()

	restored, _, err := missionruntime.RestoreDurableHarness(bundle.Runtime, parentID)
	if err != nil {
		t.Fatalf("RestoreDurableHarness(parent) error = %v", err)
	}
	launch := restored.Snapshot().CrossMissionLaunches["route-1"]
	if launch.State != missionruntime.CrossMissionLaunchLaunched || launch.ChildRunID != childID {
		t.Fatalf("recovered launch = %#v", launch)
	}
}
