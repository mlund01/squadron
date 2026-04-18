package mission

import (
	"context"
	"testing"

	"squadron/config"
)

func intp(v int64) *int64       { return &v }
func fltp(v float64) *float64   { return &v }

func TestNewBudgetTracker_NilWhenNoBudgets(t *testing.T) {
	m := &config.Mission{Tasks: []config.Task{{Name: "a"}}}
	if NewBudgetTracker(m) != nil {
		t.Fatal("expected nil tracker when no budgets are declared")
	}
}

func TestBudgetTracker_TaskTokenBreach(t *testing.T) {
	m := &config.Mission{
		Tasks: []config.Task{
			{Name: "a", Budget: &config.Budget{Tokens: intp(100)}},
		},
	}
	bt := NewBudgetTracker(m)
	if err := bt.Record("a", 50, 0); err != nil {
		t.Fatalf("unexpected breach at 50 tokens: %v", err)
	}
	err := bt.Record("a", 50, 0)
	if err == nil {
		t.Fatal("expected breach at 100 tokens")
	}
	if _, ok := err.(*BudgetBreach); !ok {
		t.Fatalf("expected *BudgetBreach, got %T", err)
	}
	// Latched: subsequent checks return the same breach.
	if bt.Check("a") == nil {
		t.Fatal("expected latched breach from Check")
	}
}

func TestBudgetTracker_MissionDollarBreach(t *testing.T) {
	m := &config.Mission{
		Budget: &config.Budget{Dollars: fltp(1.0)},
		Tasks:  []config.Task{{Name: "a"}, {Name: "b"}},
	}
	bt := NewBudgetTracker(m)
	if err := bt.Record("a", 0, 0.60); err != nil {
		t.Fatalf("unexpected breach: %v", err)
	}
	err := bt.Record("b", 0, 0.40)
	if err == nil {
		t.Fatal("expected mission dollar breach once combined cost hits $1.00")
	}
	b := err.(*BudgetBreach)
	if b.Scope != "mission" || b.Kind != "dollars" {
		t.Fatalf("unexpected breach shape: %+v", b)
	}
}

func TestBudgetTracker_IterationSuffixStripped(t *testing.T) {
	m := &config.Mission{
		Tasks: []config.Task{
			{Name: "a", Budget: &config.Budget{Tokens: intp(100)}},
		},
	}
	bt := NewBudgetTracker(m)
	// Iteration suffix must NOT create a separate per-task counter.
	if err := bt.Record("a[0]", 60, 0); err != nil {
		t.Fatalf("unexpected breach: %v", err)
	}
	if err := bt.Record("a[1]", 50, 0); err == nil {
		t.Fatal("expected breach when iterations combined cross the task budget")
	}
}

func TestBudgetTracker_SetCancelFires(t *testing.T) {
	m := &config.Mission{
		Budget: &config.Budget{Tokens: intp(10)},
		Tasks:  []config.Task{{Name: "a"}},
	}
	bt := NewBudgetTracker(m)

	ctx, cancel := context.WithCancel(context.Background())
	bt.SetCancel(cancel)
	defer cancel()

	if err := bt.Record("a", 20, 0); err == nil {
		t.Fatal("expected breach")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected context to be canceled on breach")
	}
}

func TestBudgetTracker_OnBreachFiresOnce(t *testing.T) {
	m := &config.Mission{
		Budget: &config.Budget{Tokens: intp(10)},
		Tasks:  []config.Task{{Name: "a"}},
	}
	bt := NewBudgetTracker(m)

	var calls int
	var captured *BudgetBreach
	bt.SetOnBreach(func(b *BudgetBreach) {
		calls++
		captured = b
	})

	if err := bt.Record("a", 20, 0); err == nil {
		t.Fatal("expected breach")
	}
	// Subsequent Record/Check calls must not re-fire the callback — breach is latched.
	_ = bt.Record("a", 5, 0)
	_ = bt.Check("a")
	if calls != 1 {
		t.Fatalf("expected OnBreach to fire exactly once, got %d", calls)
	}
	if captured == nil || captured.Scope != "mission" || captured.Kind != "tokens" {
		t.Fatalf("unexpected captured breach: %+v", captured)
	}
}

func TestBudgetTracker_NoLimitMeansNoBreach(t *testing.T) {
	// Dollar-only budget + model with no pricing (cost always 0) must not breach
	// purely from token usage — that's the whole reason both limits exist.
	m := &config.Mission{
		Tasks: []config.Task{
			{Name: "a", Budget: &config.Budget{Dollars: fltp(1.0)}},
		},
	}
	bt := NewBudgetTracker(m)
	for i := 0; i < 100; i++ {
		if err := bt.Record("a", 1000, 0); err != nil {
			t.Fatalf("unexpected breach on token-only usage: %v", err)
		}
	}
}
