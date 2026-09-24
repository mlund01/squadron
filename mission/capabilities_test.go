package mission

import (
	"strings"
	"testing"

	"squadron/config"
	missionruntime "squadron/mission/runtime"
)

func TestCapabilityPolicyResolveGrant(t *testing.T) {
	t.Parallel()

	m := &config.Mission{
		Name:   "research",
		Models: []string{"fast", "strong"},
		Tools:  []string{"plugins.search.all", "builtins.http.get"},
		Tasks: []config.Task{
			{
				Name:   "collect",
				Models: []string{"fast"},
				Tools:  []string{"mcp.filesystem.read_file"},
			},
			{Name: "synthesize"},
		},
	}
	policy, err := NewCapabilityPolicy(m)
	if err != nil {
		t.Fatalf("NewCapabilityPolicy() error = %v", err)
	}

	grant, err := policy.ResolveGrant("collect", "fast", []string{"plugins.search.all", "plugins.search.all"})
	if err != nil {
		t.Fatalf("ResolveGrant() error = %v", err)
	}
	if grant.TaskName != "collect" || grant.Model != "fast" {
		t.Fatalf("unexpected grant identity: %#v", grant)
	}
	wantTools := []string{"mcp.filesystem.read_file", "plugins.search.all"}
	if len(grant.Tools) != len(wantTools) {
		t.Fatalf("grant tools = %v, want %v", grant.Tools, wantTools)
	}
	for i := range wantTools {
		if grant.Tools[i] != wantTools[i] {
			t.Fatalf("grant tools = %v, want %v", grant.Tools, wantTools)
		}
	}

	if _, err := policy.ResolveGrant("collect", "strong", nil); err == nil {
		t.Fatal("ResolveGrant() accepted model excluded by task model pool")
	}
	if _, err := policy.ResolveGrant("collect", "fast", []string{"plugins.shell.exec"}); err == nil {
		t.Fatal("ResolveGrant() accepted tool outside mission capability pool")
	}
	if _, err := policy.ResolveGrant("missing", "fast", nil); err == nil {
		t.Fatal("ResolveGrant() accepted undeclared task")
	}

	if _, err := policy.ResolveGrant("synthesize", "strong", nil); err != nil {
		t.Fatalf("ResolveGrant() should inherit mission model pool: %v", err)
	}
}

func TestRuntimeGrantResolverPersistsPolicyAndRunContext(t *testing.T) {
	t.Parallel()

	m := &config.Mission{
		Name: "research", Models: []string{"fast"}, Tools: []string{"plugins.search.all"},
		Tasks: []config.Task{{Name: "collect", Tools: []string{"mcp.files.read"}}},
	}
	resolver, err := NewRuntimeGrantResolver(m, ExecutionGrantContext{
		RunAs: "service-principal:crawler", InputsJSON: `{"topic":"durability"}`, ConfigurationVersion: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("NewRuntimeGrantResolver() error = %v", err)
	}
	grant, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{
		TaskName: "collect", Model: "fast", Tools: []string{"plugins.search.all"},
	})
	if err != nil {
		t.Fatalf("ResolveExecutionGrant() error = %v", err)
	}
	if grant.RunAs != "service-principal:crawler" || grant.ConfigurationVersion != "sha256:abc" || grant.InputsJSON == "" {
		t.Fatalf("grant context = %#v", grant)
	}
	if !strings.Contains(grant.PolicyJSON, "mcp.files.read") || !strings.Contains(grant.PolicyJSON, "plugins.search.all") {
		t.Fatalf("policy snapshot = %s", grant.PolicyJSON)
	}
	if _, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{
		TaskName: "collect", Model: "fast", Tools: []string{"start_task"},
	}); err == nil {
		t.Fatal("resolver granted a commander control to an executor")
	}
}

func TestCommanderControlSurfaceContainsOnlyIntrinsicOperations(t *testing.T) {
	t.Parallel()

	want := []CommanderControlOperation{
		ControlInspectMission,
		ControlStartTask,
		ControlInspectTask,
		ControlSelectRoute,
		ControlFinishMission,
	}
	got := CommanderControlSurface()
	if len(got) != len(want) {
		t.Fatalf("control surface = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("control surface = %v, want %v", got, want)
		}
	}
}

func TestRuntimeTaskDefinitionsPreserveOptionalOutputContract(t *testing.T) {
	t.Parallel()

	m := &config.Mission{Tasks: []config.Task{{
		Name: "summarize", RawObjective: "Summarize the source", DependsOn: []string{"collect"}, MaxRetries: 2,
		SendTo: []string{"publish"},
		Router: &config.TaskRouter{Routes: []config.TaskRoute{{Target: "review", Condition: "Review required"}}},
		Output: &config.OutputSchema{Fields: []config.OutputField{
			{Name: "summary", Type: "string", Required: true},
			{Name: "citations", Type: "array", Items: &config.OutputField{Type: "string"}},
		}},
	}}}
	definitions, err := RuntimeTaskDefinitions(m)
	if err != nil {
		t.Fatalf("RuntimeTaskDefinitions() error = %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("definitions = %#v", definitions)
	}
	definition := definitions[0]
	if definition.Objective != "Summarize the source" || len(definition.DependsOn) != 1 || definition.MaxRetries != 2 || len(definition.SendTo) != 1 || len(definition.Routes) != 1 {
		t.Fatalf("definition = %#v", definition)
	}
	fields := definition.ResultPolicy.Fields
	if len(fields) != 2 || fields[0].Name != "summary" || !fields[0].Required || fields[1].Items == nil || fields[1].Items.Type != "string" {
		t.Fatalf("result fields = %#v", fields)
	}
}
