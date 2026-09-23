package agent

import (
	"squadron/aitools"
	"squadron/llm"
	"strings"
	"testing"
)

func TestRoutePromptIncludesDestinationInputs(t *testing.T) {
	commander := &Commander{session: llm.NewSession(nil, "test")}
	commander.injectRouteOptions([]aitools.RouteOption{
		{Target: "next_task", Condition: "needs more work"},
		{Target: "fix", IsMission: true, Condition: "defect established", Inputs: []aitools.RouteInput{
			{Name: "evidence", Type: "string", Required: true, Description: "Citations supporting the diagnosis"},
			{Name: "messageable", Type: "bool", Description: "Whether the owner can continue"},
		}},
	})
	prompt := strings.Join(commander.session.GetSystemPrompts(), "\n")
	for _, expected := range []string{"next_task", "fix", "`evidence` (string, required)", "Citations supporting the diagnosis", "`messageable` (bool, optional)", "mission_inputs", "not copied automatically", "starts the destination mission"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("routing prompt missing %q", expected)
		}
	}
}
