package mission

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"squadron/config"
)

// BudgetScope identifies whether a breach is against a task's or the mission's budget.
type BudgetScope string

const (
	BudgetScopeMission BudgetScope = "mission"
	BudgetScopeTask    BudgetScope = "task"
)

// BudgetKind identifies which budget dimension was exceeded.
type BudgetKind string

const (
	BudgetKindTokens  BudgetKind = "tokens"
	BudgetKindDollars BudgetKind = "dollars"
)

// BudgetBreach describes which budget limit was exceeded.
type BudgetBreach struct {
	Scope    BudgetScope
	TaskName string // populated when Scope == BudgetScopeTask
	Kind     BudgetKind
	Limit    float64 // configured limit
	Used     float64 // cumulative usage at the moment of breach
}

func (b *BudgetBreach) Error() string {
	if b.Scope == BudgetScopeMission {
		if b.Kind == BudgetKindTokens {
			return fmt.Sprintf("mission budget exceeded: %.0f tokens used, limit %.0f", b.Used, b.Limit)
		}
		return fmt.Sprintf("mission budget exceeded: $%.4f used, limit $%.4f", b.Used, b.Limit)
	}
	if b.Kind == BudgetKindTokens {
		return fmt.Sprintf("task '%s' budget exceeded: %.0f tokens used, limit %.0f", b.TaskName, b.Used, b.Limit)
	}
	return fmt.Sprintf("task '%s' budget exceeded: $%.4f used, limit $%.4f", b.TaskName, b.Used, b.Limit)
}

// BudgetTracker tracks cumulative token/cost usage against mission- and task-scoped
// budgets. Safe for concurrent use. Once any budget is breached the tracker latches
// into the breached state and every subsequent Check/Record returns the same breach.
type BudgetTracker struct {
	mu            sync.Mutex
	missionBudget *config.Budget
	taskBudgets   map[string]*config.Budget

	missionTokens int64
	missionCost   float64
	taskTokens    map[string]int64
	taskCost      map[string]float64

	breach   *BudgetBreach
	cancel   context.CancelFunc
	onBreach func(*BudgetBreach)
}

// NewBudgetTracker builds a tracker from a mission's declared budgets.
// Returns nil if neither the mission nor any task declares a budget — callers
// should tolerate a nil tracker to keep hot paths allocation-free when budgeting is unused.
func NewBudgetTracker(mission *config.Mission) *BudgetTracker {
	if mission == nil {
		return nil
	}
	taskBudgets := make(map[string]*config.Budget)
	for _, t := range mission.Tasks {
		if t.Budget != nil {
			taskBudgets[t.Name] = t.Budget
		}
	}
	if mission.Budget == nil && len(taskBudgets) == 0 {
		return nil
	}
	return &BudgetTracker{
		missionBudget: mission.Budget,
		taskBudgets:   taskBudgets,
		taskTokens:    make(map[string]int64),
		taskCost:      make(map[string]float64),
	}
}

// SetCancel registers a cancel function that the tracker will invoke on first breach
// so in-flight LLM and tool calls across the mission unwind promptly.
func (bt *BudgetTracker) SetCancel(cancel context.CancelFunc) {
	if bt == nil {
		return
	}
	bt.mu.Lock()
	bt.cancel = cancel
	bt.mu.Unlock()
}

// SetOnBreach registers a callback invoked exactly once, when the first breach
// latches. Used by the runner to emit a mission_issue event at breach time so
// the observability signal lines up with the moment usage crosses the limit
// (rather than later, when the error bubbles up to the runner).
func (bt *BudgetTracker) SetOnBreach(fn func(*BudgetBreach)) {
	if bt == nil {
		return
	}
	bt.mu.Lock()
	bt.onBreach = fn
	bt.mu.Unlock()
}

// baseTaskName strips the `[N]` iteration suffix so every iteration of an iterated
// task shares the same per-task budget.
func baseTaskName(taskName string) string {
	if idx := strings.LastIndex(taskName, "["); idx != -1 {
		return taskName[:idx]
	}
	return taskName
}

// Check returns the latched breach error without modifying usage counters.
// Commanders and agents call this immediately before issuing an LLM request.
func (bt *BudgetTracker) Check(taskName string) error {
	if bt == nil {
		return nil
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if bt.breach != nil {
		return bt.breach
	}
	return nil
}

// Record adds the given usage to the task and mission counters and returns a breach
// error if any limit has been reached. The first breach wins — subsequent calls
// return that same breach. Pass the raw task name (iteration suffixes are stripped).
func (bt *BudgetTracker) Record(taskName string, tokens int, cost float64) error {
	if bt == nil {
		return nil
	}
	bt.mu.Lock()
	if bt.breach != nil {
		b := bt.breach
		bt.mu.Unlock()
		return b
	}

	base := baseTaskName(taskName)
	bt.missionTokens += int64(tokens)
	bt.missionCost += cost
	bt.taskTokens[base] += int64(tokens)
	bt.taskCost[base] += cost

	breach := bt.detectBreachLocked(base)
	if breach == nil {
		bt.mu.Unlock()
		return nil
	}
	bt.breach = breach
	cb, cancel := bt.onBreach, bt.cancel
	bt.mu.Unlock()

	// Side effects fire outside bt.mu so user callbacks that acquire other locks
	// (e.g. a streamer mutex via MissionIssue) can't invert the lock order.
	if cb != nil {
		cb(breach)
	}
	if cancel != nil {
		cancel()
	}
	return breach
}

func (bt *BudgetTracker) detectBreachLocked(base string) *BudgetBreach {
	if tb, ok := bt.taskBudgets[base]; ok {
		if tb.Tokens != nil && bt.taskTokens[base] >= *tb.Tokens {
			return &BudgetBreach{
				Scope: BudgetScopeTask, TaskName: base, Kind: BudgetKindTokens,
				Limit: float64(*tb.Tokens), Used: float64(bt.taskTokens[base]),
			}
		}
		if tb.Dollars != nil && bt.taskCost[base] >= *tb.Dollars {
			return &BudgetBreach{
				Scope: BudgetScopeTask, TaskName: base, Kind: BudgetKindDollars,
				Limit: *tb.Dollars, Used: bt.taskCost[base],
			}
		}
	}
	if mb := bt.missionBudget; mb != nil {
		if mb.Tokens != nil && bt.missionTokens >= *mb.Tokens {
			return &BudgetBreach{
				Scope: BudgetScopeMission, Kind: BudgetKindTokens,
				Limit: float64(*mb.Tokens), Used: float64(bt.missionTokens),
			}
		}
		if mb.Dollars != nil && bt.missionCost >= *mb.Dollars {
			return &BudgetBreach{
				Scope: BudgetScopeMission, Kind: BudgetKindDollars,
				Limit: *mb.Dollars, Used: bt.missionCost,
			}
		}
	}
	return nil
}

// Breach returns the latched breach, or nil if no budget has been exceeded.
func (bt *BudgetTracker) Breach() *BudgetBreach {
	if bt == nil {
		return nil
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return bt.breach
}

// BudgetChecker is the narrow interface commanders/agents use to participate in
// budget enforcement. An implementation is produced by Tracker.For(taskName).
type BudgetChecker interface {
	CheckBudget() error
	RecordUsage(tokens int, cost float64) error
}

// For returns a per-task BudgetChecker bound to this tracker. Returns nil when
// the tracker itself is nil so callers can unconditionally pass it through.
func (bt *BudgetTracker) For(taskName string) BudgetChecker {
	if bt == nil {
		return nil
	}
	return &taskBudgetChecker{tracker: bt, taskName: taskName}
}

type taskBudgetChecker struct {
	tracker  *BudgetTracker
	taskName string
}

func (c *taskBudgetChecker) CheckBudget() error {
	return c.tracker.Check(c.taskName)
}

func (c *taskBudgetChecker) RecordUsage(tokens int, cost float64) error {
	return c.tracker.Record(c.taskName, tokens, cost)
}
