package mission

import (
	"context"
	"github.com/zclconf/go-cty/cty"
	"squadron/aitools"
	"squadron/config"
	"strings"
	"testing"
)

func TestMissionRouteUsesDestinationParserBeforeCompletion(t *testing.T) {
	defaultValue := cty.StringVal("default")
	destination := config.Mission{Name: "next", Inputs: []config.MissionInput{
		{Name: "attempt", Type: "integer"},
		{Name: "live", Type: "bool"},
		{Name: "optional", Type: "string", Default: &defaultValue},
	}}
	runner := &Runner{cfg: &config.Config{Missions: []config.Mission{destination}}}
	task := config.Task{Router: &config.TaskRouter{Routes: []config.TaskRoute{{Target: "next", IsMission: true}}}}
	routes := runner.routeOptionsForTask(task)
	tool := &aitools.TaskCompleteTool{Routes: routes}
	// These values normalize to strings, but cannot be consumed by the destination.
	result := tool.Call(context.Background(), `{"route":"next","mission_inputs":{"attempt":"fraction","live":"unknown"}}`)
	if tool.IsCompleted() || !strings.Contains(result, "mission_inputs.attempt") || !strings.Contains(result, "mission_inputs.live") {
		t.Fatal(result)
	}
	result = tool.Call(context.Background(), `{"route":"next","mission_inputs":{"attempt":2,"live":false}}`)
	if !tool.IsCompleted() || tool.ChosenRoute() != "next" {
		t.Fatal(result)
	}
	resolved, err := destination.ResolveInputValues(tool.MissionInputs())
	if err != nil {
		t.Fatal(err)
	}
	if resolved["live"].True() || resolved["optional"].AsString() != "default" {
		t.Fatal("destination inputs changed")
	}
}

func TestMissionRouteOmitsProtectedInputs(t *testing.T) {
	destination := config.Mission{Name: "next", Inputs: []config.MissionInput{
		{Name: "token", Type: "string", Protected: true},
		{Name: "topic", Type: "string"},
	}}
	runner := &Runner{cfg: &config.Config{Missions: []config.Mission{destination}}}
	task := config.Task{Router: &config.TaskRouter{Routes: []config.TaskRoute{{Target: "next", IsMission: true}}}}
	routes := runner.routeOptionsForTask(task)
	if len(routes[0].Inputs) != 1 || routes[0].Inputs[0].Name != "topic" {
		t.Fatalf("route inputs = %#v", routes[0].Inputs)
	}
	tool := &aitools.TaskCompleteTool{Routes: routes}
	result := tool.Call(context.Background(), `{"route":"next","mission_inputs":{"topic":"x","token":"guess"}}`)
	if tool.IsCompleted() || !strings.Contains(result, "mission_inputs.token is not declared") {
		t.Fatal(result)
	}
}
