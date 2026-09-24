package aitools_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"squadron/aitools"
	"squadron/config"
)

func routeFixture() (*aitools.TaskCompleteTool, *config.Mission) {
	fallback := cty.StringVal("main")
	destination := &config.Mission{Inputs: []config.MissionInput{
		{Name: "evidence", Type: "string", Description: "Cited investigation result"},
		{Name: "messageable", Type: "bool"},
		{Name: "attempt", Type: "integer"},
		{Name: "ratio", Type: "number"},
		{Name: "cases", Type: "list"},
		{Name: "context", Type: "object"},
		{Name: "labels", Type: "map"},
		{Name: "branch", Type: "string", Default: &fallback},
	}}
	route := aitools.RouteOption{Target: "fix", IsMission: true}
	for _, input := range destination.Inputs {
		route.Inputs = append(route.Inputs, aitools.RouteInput{Name: input.Name, Type: input.Type,
			Description: input.Description, Required: input.Default == nil, Validate: input.ValidateValue})
	}
	return &aitools.TaskCompleteTool{Routes: []aitools.RouteOption{route}}, destination
}

const typedCompletion = `{"route":"fix","summary":"accepted evidence","mission_inputs":{"evidence":"file.sql:42","messageable":false,"attempt":0,"ratio":1.25,"cases":["A",2],"context":{"nested":{"active":true}},"labels":{"one":"two"}}}`

func response(t *testing.T, result string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("invalid JSON response %q: %v", result, err)
	}
	return decoded
}

func TestTaskCompleteTypedMissionInputs(t *testing.T) {
	tool, destination := routeFixture()
	if got := response(t, tool.Call(context.Background(), typedCompletion)); got["status"] != "ok" {
		t.Fatal(got)
	}
	if !tool.IsCompleted() || !tool.IsMissionRoute() || tool.ChosenRoute() != "fix" {
		t.Fatal("mission route not committed")
	}
	want := map[string]string{"evidence": "file.sql:42", "messageable": "false", "attempt": "0", "ratio": "1.25", "cases": `["A",2]`, "context": `{"nested":{"active":true}}`, "labels": `{"one":"two"}`}
	if !reflect.DeepEqual(tool.MissionInputs(), want) {
		t.Fatalf("inputs = %#v, want %#v", tool.MissionInputs(), want)
	}
	resolved, err := destination.ResolveInputValues(tool.MissionInputs())
	if err != nil {
		t.Fatal(err)
	}
	if resolved["messageable"].True() || resolved["branch"].AsString() != "main" {
		t.Fatal("false or optional default lost")
	}

	// String-encoded values must resolve and replay identically to native JSON values.
	legacy, _ := json.Marshal(map[string]any{"route": "fix", "summary": "accepted evidence", "mission_inputs": want})
	legacyTool, _ := routeFixture()
	if got := response(t, legacyTool.Call(context.Background(), string(legacy))); got["status"] != "ok" {
		t.Fatal(got)
	}
	for _, payload := range []string{typedCompletion, string(legacy)} {
		restored, _ := routeFixture()
		restored.SubtaskChecker = func() (int, int) { return 1, 1 }
		restored.ApplyStateFromSuccessfulInput(payload)
		if !restored.IsCompleted() || !restored.IsSucceeded() || !restored.IsMissionRoute() || restored.Summary() != "accepted evidence" || !reflect.DeepEqual(restored.MissionInputs(), want) {
			t.Fatalf("replay lost state: %#v", restored)
		}
	}
}

func TestTaskCompleteRejectsAllInvalidInputsBeforeCommitting(t *testing.T) {
	tool, _ := routeFixture()
	payload := `{"route":"fix","summary":"must not be stored","mission_inputs":{"evidence":42,"messageable":"perhaps","attempt":1.5,"ratio":"invalid","cases":{},"context":[],"labels":null,"typo":true}}`
	got := response(t, tool.Call(context.Background(), payload))
	if got["status"] != "error" {
		t.Fatal(got)
	}
	for _, name := range []string{"evidence", "messageable", "attempt", "ratio", "cases", "context", "labels", "typo"} {
		if !strings.Contains(got["error"].(string), "mission_inputs."+name) {
			t.Errorf("missing diagnostic for %s: %v", name, got)
		}
	}
	if tool.IsCompleted() || tool.IsSucceeded() || tool.IsMissionRoute() || tool.ChosenRoute() != "" || tool.Summary() != "" || tool.MissionInputs() != nil {
		t.Fatal("rejected call mutated completion state")
	}
	if got := response(t, tool.Call(context.Background(), typedCompletion)); got["status"] != "ok" {
		t.Fatal("corrected retry failed", got)
	}
}

func TestTaskCompleteReportsMissingInputsTogether(t *testing.T) {
	tool, _ := routeFixture()
	got := response(t, tool.Call(context.Background(), `{"route":"fix"}`))
	for _, name := range []string{"evidence", "messageable", "attempt", "ratio", "cases", "context", "labels"} {
		if !strings.Contains(got["error"].(string), "mission_inputs."+name) {
			t.Errorf("missing %s: %v", name, got)
		}
	}
	if strings.Contains(got["error"].(string), "mission_inputs.branch") {
		t.Fatal("defaulted input treated as required")
	}
}

func TestTaskCompleteMalformedCallsDoNotBecomeMissingRoute(t *testing.T) {
	for _, payload := range []string{`{`, `null`, `[]`, `{"route":true}`, `{"summary":[]}`, `{"succeed":"false"}`, `{"route":"fix","mission_inputs":[]}`} {
		t.Run(payload, func(t *testing.T) {
			tool, _ := routeFixture()
			got := response(t, tool.Call(context.Background(), payload))
			if got["status"] != "error" || !strings.Contains(got["error"].(string), "Invalid task_complete arguments") {
				t.Fatal(got)
			}
			if tool.IsCompleted() || tool.Summary() != "" {
				t.Fatal("malformed call changed state")
			}
		})
	}
}

func TestTaskCompleteRejectsEmptyRequiredStrings(t *testing.T) {
	for _, value := range []string{`""`, `"   "`, `null`} {
		tool := &aitools.TaskCompleteTool{Routes: []aitools.RouteOption{{Target: "fix", IsMission: true, Inputs: []aitools.RouteInput{{Name: "evidence", Type: "string", Required: true}}}}}
		got := response(t, tool.Call(context.Background(), `{"route":"fix","mission_inputs":{"evidence":`+value+`}}`))
		if got["status"] != "error" || tool.IsCompleted() {
			t.Fatal(got)
		}
	}
}

func TestTaskCompleteValidatesOnlySelectedDestination(t *testing.T) {
	tool, _ := routeFixture()
	tool.Routes = append(tool.Routes, aitools.RouteOption{Target: "other", IsMission: true, Inputs: []aitools.RouteInput{{Name: "other_required", Type: "string", Required: true}}})
	if got := response(t, tool.Call(context.Background(), typedCompletion)); got["status"] != "ok" {
		t.Fatal(got)
	}
}

func TestTaskCompleteNonMissionRoutes(t *testing.T) {
	for _, route := range []string{"next_task", "none"} {
		tool, _ := routeFixture()
		tool.Routes = append(tool.Routes, aitools.RouteOption{Target: "next_task"})
		got := response(t, tool.Call(context.Background(), `{"route":"`+route+`","summary":"done"}`))
		if got["status"] != "ok" || !tool.IsCompleted() || tool.IsMissionRoute() || tool.Summary() != "done" {
			t.Fatal(got)
		}
	}
}

func TestTaskCompletePreservesNumberText(t *testing.T) {
	tool := &aitools.TaskCompleteTool{Routes: []aitools.RouteOption{{Target: "next", IsMission: true, Inputs: []aitools.RouteInput{{Name: "n", Type: "number", Required: true}}}}}
	got := response(t, tool.Call(context.Background(), `{"route":"next","mission_inputs":{"n":9007199254740993}}`))
	if got["status"] != "ok" || tool.MissionInputs()["n"] != "9007199254740993" {
		t.Fatal(got, tool.MissionInputs())
	}
}

func TestTaskCompleteRejectsNonFiniteEncodedNumbers(t *testing.T) {
	for _, kind := range []string{"number", "integer"} {
		for _, value := range []string{"NaN", "+Inf", "-Inf"} {
			input := config.MissionInput{Name: "value", Type: kind}
			tool := &aitools.TaskCompleteTool{Routes: []aitools.RouteOption{{Target: "next", IsMission: true, Inputs: []aitools.RouteInput{{Name: input.Name, Type: input.Type, Validate: input.ValidateValue}}}}}
			payload, _ := json.Marshal(map[string]any{"route": "next", "mission_inputs": map[string]string{"value": value}})
			if got := response(t, tool.Call(context.Background(), string(payload))); got["status"] != "error" || tool.IsCompleted() {
				t.Fatal(kind, value, got)
			}
		}
	}
}

func TestTaskCompleteAcceptsNativeFileEnvelope(t *testing.T) {
	input := config.MissionInput{Name: "doc", Type: "file"}
	tool := &aitools.TaskCompleteTool{Routes: []aitools.RouteOption{{Target: "next", IsMission: true, Inputs: []aitools.RouteInput{{Name: input.Name, Type: input.Type, Required: true, Validate: input.ValidateValue}}}}}
	got := response(t, tool.Call(context.Background(), `{"route":"next","mission_inputs":{"doc":{"filename":"a.md","content_base64":"aGk="}}}`))
	if got["status"] != "ok" || tool.MissionInputs()["doc"] != `{"filename":"a.md","content_base64":"aGk="}` {
		t.Fatal(got, tool.MissionInputs())
	}
	for _, value := range []string{`true`, `42`, `["a.md"]`} {
		retry := &aitools.TaskCompleteTool{Routes: tool.Routes}
		if got := response(t, retry.Call(context.Background(), `{"route":"next","mission_inputs":{"doc":`+value+`}}`)); got["status"] != "error" {
			t.Fatal(value, got)
		}
	}
}
