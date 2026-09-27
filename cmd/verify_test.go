package cmd

import (
	"bytes"
	"strings"
	"testing"

	"squadron/config"
)

func TestPrintMissionSummaryShowsExecutorPolicy(t *testing.T) {
	mission := config.Mission{
		Name:      "pipeline",
		Commander: &config.MissionCommander{Model: "commander_model"},
		Models:    []string{"fast", "strong"},
		Tools:     []string{"plugins.search.all"},
		Tasks: []config.Task{
			{Name: "research", Tools: []string{"mcp.docs.read"}},
			{Name: "report", Models: []string{"strong"}, DependsOn: []string{"research"}},
		},
	}
	var output bytes.Buffer
	printMissionSummary(&output, []config.Mission{mission})
	for _, want := range []string{
		"pipeline (commander: commander_model, tasks: 2)",
		"Executor model pool: [fast strong]; optional tool pool: [plugins.search.all]",
		"research (models: mission pool; required tools: [mcp.docs.read])",
		"report (models: [strong]; required tools: []) [depends_on: [research]]",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "agents:") {
		t.Errorf("summary contains obsolete task-agent framing:\n%s", output.String())
	}
}
