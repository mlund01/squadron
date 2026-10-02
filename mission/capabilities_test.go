package mission

import (
	"reflect"
	"strings"
	"testing"

	"squadron/config"
	missionruntime "squadron/mission/runtime"

	"github.com/zclconf/go-cty/cty"
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

func TestCapabilityPolicyGrantsOnlyDeclaredStorageSlots(t *testing.T) {
	t.Parallel()
	m := &config.Mission{
		Name: "research", Models: []string{"fast"}, Scratchpad: true,
		Tasks: []config.Task{{Name: "collect", Packets: []string{"sources"}}, {Name: "summarize"}},
	}
	policy, err := NewCapabilityPolicy(m)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := policy.ResolveGrant("collect", "fast", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"file_list", "file_read", "file_view", "file_search", "file_grep", "file_create", "file_delete"} {
		if !containsString(grant.Tools, tool) {
			t.Fatalf("storage tool %q missing from durable grant: %v", tool, grant.Tools)
		}
	}
	m.Scratchpad = false
	grant, err = policy.ResolveGrant("collect", "fast", nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(grant.Tools, "file_create") || !containsString(grant.Tools, "file_read") {
		t.Fatalf("packet-only grant should be read-only: %v", grant.Tools)
	}
	grant, err = policy.ResolveGrant("summarize", "fast", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(grant.Tools) != 0 {
		t.Fatalf("task inherited a sibling's packet tools: %v", grant.Tools)
	}
}

func TestRuntimeCommanderPoliciesExposeExactGrantKeys(t *testing.T) {
	t.Parallel()
	m := &config.Mission{
		Models: []string{"claude_haiku_4_5", "gpt_5"},
		Tools:  []string{"plugins.search.all"},
		Tasks: []config.Task{
			{Name: "draft", Tools: []string{"mcp.files.read"}},
			{Name: "polish", Models: []string{"gpt_5"}},
		},
	}
	policies := runtimeCommanderPolicies(m)
	models := policies["task_allowed_models"].(map[string][]string)
	if got := strings.Join(models["draft"], ","); got != "claude_haiku_4_5,gpt_5" {
		t.Fatalf("inherited model keys = %q", got)
	}
	if got := strings.Join(models["polish"], ","); got != "gpt_5" {
		t.Fatalf("task model keys = %q", got)
	}
	tools := policies["task_tools"].(map[string][]string)
	if got := strings.Join(tools["draft"], ","); got != "mcp.files.read" {
		t.Fatalf("mandatory task tools = %q", got)
	}
	if got := strings.Join(policies["optional_mission_tools"].([]string), ","); got != "plugins.search.all" {
		t.Fatalf("optional mission tools = %q", got)
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
	redundant, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{
		TaskName: "collect", Model: "fast", Tools: []string{"mcp.files.read", "plugins.search.all"},
	})
	if err != nil {
		t.Fatalf("redundant task tool was rejected: %v", err)
	}
	if !reflect.DeepEqual(redundant.Tools, []string{"mcp.files.read", "plugins.search.all"}) {
		t.Fatalf("redundant task tool was not deduplicated: %q", redundant.Tools)
	}
	if _, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{
		TaskName: "collect", Model: "fast", Tools: []string{"start_task"},
	}); err == nil {
		t.Fatal("resolver granted a commander control to an executor")
	}
}

func TestIterationChildUsesParentCapabilityPolicy(t *testing.T) {
	m := &config.Mission{
		Name: "iteration", Models: []string{"fast", "strong"}, Tools: []string{"builtins.http.get"},
		Tasks: []config.Task{{
			Name: "work", RawObjective: "Process an item", Models: []string{"fast"},
			Tools:    []string{"mcp.files.read"},
			Iterator: &config.TaskIterator{Dataset: "items", Parallel: true, ConcurrencyLimit: 2, StartDelay: 50},
		}},
	}
	definitions, err := RuntimeTaskDefinitions(m)
	if err != nil {
		t.Fatal(err)
	}
	if definitions[0].Iterator == nil || definitions[0].Iterator.StartDelayMS != 50 || definitions[0].Iterator.ConcurrencyLimit != 2 {
		t.Fatalf("runtime iterator policy = %#v", definitions[0].Iterator)
	}
	resolver, err := NewRuntimeGrantResolver(m, ExecutionGrantContext{RunAs: "user:one", ConfigurationVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{TaskName: "work[0]", Model: "fast", Tools: []string{"builtins.http.get"}})
	if err != nil {
		t.Fatal(err)
	}
	if grant.TaskName != "work[0]" || len(grant.Tools) != 2 || grant.Tools[0] != "mcp.files.read" {
		t.Fatalf("child grant = %#v", grant)
	}
	if _, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{TaskName: "work[1]", Model: "strong"}); err == nil {
		t.Fatal("child escaped parent model policy")
	}
	if _, err := resolver.ResolveExecutionGrant(missionruntime.GrantRequest{TaskName: "work[-1]", Model: "fast"}); err == nil {
		t.Fatal("noncanonical child index was accepted")
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
		Name: "summarize", RawObjective: "Summarize the source", DependsOn: []string{"collect"}, MaxRetries: 2, RetryDelaySeconds: 30,
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
	if definition.Objective != "Summarize the source" || len(definition.DependsOn) != 1 || definition.MaxRetries != 2 || definition.RetryDelaySeconds != 30 || len(definition.SendTo) != 1 || len(definition.Routes) != 1 {
		t.Fatalf("definition = %#v", definition)
	}
	fields := definition.ResultPolicy.Fields
	if len(fields) != 2 || fields[0].Name != "summary" || !fields[0].Required || fields[1].Items == nil || fields[1].Items.Type != "string" {
		t.Fatalf("result fields = %#v", fields)
	}
}

func TestRuntimeTaskDefinitionsExposeOnlyPublicDestinationInputs(t *testing.T) {
	t.Parallel()
	source := &config.Mission{Tasks: []config.Task{{
		Name: "classify", Router: &config.TaskRouter{Routes: []config.TaskRoute{{Target: "follow_up", IsMission: true, Condition: "needs follow-up"}}},
	}}}
	destinations := []config.Mission{{
		Name: "follow_up",
		Inputs: []config.MissionInput{
			{Name: "topic", Type: config.InputTypeString, Description: "Subject"},
			{Name: "attempt", Type: config.InputTypeInteger, Default: ctyValuePtr(cty.NumberIntVal(1))},
			{Name: "token", Type: config.InputTypeString, Protected: true},
		},
	}}
	definitions, err := RuntimeTaskDefinitionsWithMissions(source, destinations)
	if err != nil {
		t.Fatalf("RuntimeTaskDefinitionsWithMissions() error = %v", err)
	}
	inputs := definitions[0].Routes[0].Inputs
	if len(inputs) != 2 || inputs[0].Name != "topic" || !inputs[0].Required || inputs[1].Name != "attempt" || inputs[1].Required {
		t.Fatalf("route inputs = %#v", inputs)
	}
}

func ctyValuePtr(value cty.Value) *cty.Value { return &value }
