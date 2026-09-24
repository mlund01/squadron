package runtime

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildIterationPlanFreezesStableChildren(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{ "b": 2, "a": 1 }`),
		json.RawMessage(`{"name":"second"}`),
	}
	policy := IterationPolicy{Dataset: "items", Parallel: true, ConcurrencyLimit: 3, Smoketest: true}
	first, err := BuildIterationPlan("process", policy, items)
	if err != nil {
		t.Fatalf("BuildIterationPlan() error = %v", err)
	}
	second, err := BuildIterationPlan("process", policy, []json.RawMessage{json.RawMessage(`{"a":1,"b":2}`), items[1]})
	if err != nil {
		t.Fatalf("BuildIterationPlan(replay) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent datasets produced different plans:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if string(first.Children[0].Item) != `{"a":1,"b":2}` || first.Children[0].Name != "process[0]" {
		t.Fatalf("first child = %#v", first.Children[0])
	}
	if !reflect.DeepEqual(first.Children[1].DependsOn, []string{"process[0]"}) {
		t.Fatalf("smoketest gate = %#v", first.Children[1].DependsOn)
	}
}

func TestBuildIterationPlanSequentialOrderAndDefaults(t *testing.T) {
	plan, err := BuildIterationPlan("process", IterationPolicy{Dataset: "items"}, []json.RawMessage{
		json.RawMessage(`1`), json.RawMessage(`2`), json.RawMessage(`3`),
	})
	if err != nil {
		t.Fatalf("BuildIterationPlan() error = %v", err)
	}
	if plan.Policy.ConcurrencyLimit != 1 {
		t.Fatalf("sequential concurrency = %d, want 1", plan.Policy.ConcurrencyLimit)
	}
	for index := 1; index < len(plan.Children); index++ {
		want := []string{plan.Children[index-1].Name}
		if !reflect.DeepEqual(plan.Children[index].DependsOn, want) {
			t.Fatalf("child %d dependencies = %#v, want %#v", index, plan.Children[index].DependsOn, want)
		}
	}
}

func TestBuildIterationPlanEmptyDatasetIsStable(t *testing.T) {
	plan, err := BuildIterationPlan("process", IterationPolicy{Dataset: "items", Parallel: true}, nil)
	if err != nil {
		t.Fatalf("BuildIterationPlan() error = %v", err)
	}
	if len(plan.Children) != 0 || plan.DatasetDigest == "" || plan.Policy.ConcurrencyLimit != 5 {
		t.Fatalf("empty plan = %#v", plan)
	}
}

func TestBuildIterationPlanRejectsInvalidPolicyAndItems(t *testing.T) {
	if _, err := BuildIterationPlan("process", IterationPolicy{Dataset: "items", Smoketest: true}, nil); err == nil {
		t.Fatal("sequential smoketest policy was accepted")
	}
	if _, err := BuildIterationPlan("process", IterationPolicy{Dataset: "items"}, []json.RawMessage{json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid dataset item was accepted")
	}
}
