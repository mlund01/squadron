package mcphost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRunMissionPassesThreadSeparatelyFromInputs(t *testing.T) {
	for _, supplied := range []string{"", "case-123"} {
		t.Run(supplied, func(t *testing.T) {
			h := &handlers{deps: Deps{RunURL: func(id string) string { return "https://cc.example/instances/1/runs/" + id }, RunMission: func(name string, inputs map[string]string, threadID string) (string, string, error) {
				if name != "triage" || inputs["issue"] != "DEV-1" || threadID != supplied {
					t.Fatalf("start args: %s %v %s", name, inputs, threadID)
				}
				if _, ok := inputs["thread_id"]; ok {
					t.Fatal("thread became a mission input")
				}
				if threadID == "" {
					threadID = "generated-thread"
				}
				return "run-1", threadID, nil
			}}}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"name": "triage", "inputs": map[string]any{"issue": "DEV-1"}, "thread_id": supplied}
			result, err := h.runMission(context.Background(), req)
			if err != nil || result.IsError {
				t.Fatalf("result: %+v %v", result, err)
			}
			var body struct {
				RunURL    string `json:"runUrl"`
				MissionID string `json:"missionId"`
				ThreadID  string `json:"threadId"`
			}
			if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &body); err != nil {
				t.Fatal(err)
			}
			want := supplied
			if want == "" {
				want = "generated-thread"
			}
			if body.MissionID != "run-1" || body.ThreadID != want || body.RunURL != "https://cc.example/instances/1/runs/run-1" {
				t.Fatalf("identities: %+v", body)
			}
		})
	}
}
