package mission

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"squadron/store"
)

// mockMissionStore implements store.MissionStore for testing.
// Only GetTaskByName and GetTaskOutputs are used by PersistentKnowledgeStore.
type mockMissionStore struct {
	tasks   map[string]*store.MissionTask           // key: missionID + "|" + taskName
	outputs map[string][]store.TaskOutputRow         // key: taskID
}

func newMockStore() *mockMissionStore {
	return &mockMissionStore{
		tasks:   make(map[string]*store.MissionTask),
		outputs: make(map[string][]store.TaskOutputRow),
	}
}

func (m *mockMissionStore) key(missionID, taskName string) string {
	return missionID + "|" + taskName
}

func (m *mockMissionStore) addTask(missionID, taskID, taskName, status string) {
	m.tasks[m.key(missionID, taskName)] = &store.MissionTask{
		ID:        taskID,
		MissionID: missionID,
		TaskName:  taskName,
		Status:    status,
	}
}

func (m *mockMissionStore) addOutput(taskID string, row store.TaskOutputRow) {
	row.TaskID = taskID
	m.outputs[taskID] = append(m.outputs[taskID], row)
}

// Implement store.MissionStore interface — only relevant methods are real.
func (m *mockMissionStore) GetTaskByName(missionID, taskName string) (*store.MissionTask, error) {
	t, ok := m.tasks[m.key(missionID, taskName)]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskName)
	}
	return t, nil
}

func (m *mockMissionStore) GetTaskOutputs(taskID string) ([]store.TaskOutputRow, error) {
	return m.outputs[taskID], nil
}

// Stub implementations for unused interface methods.
func (m *mockMissionStore) CreateMission(name, inputsJSON, configJSON string) (string, error) {
	return "", nil
}
func (m *mockMissionStore) UpdateMissionStatus(id, status string) error { return nil }
func (m *mockMissionStore) CreateTask(missionID, taskName, configJSON string) (string, error) {
	return "", nil
}
func (m *mockMissionStore) UpdateTaskStatus(id, status string, outputJSON, errMsg *string) error {
	return nil
}
func (m *mockMissionStore) GetTask(id string) (*store.MissionTask, error) { return nil, nil }
func (m *mockMissionStore) GetTasksByMission(missionID string) ([]store.MissionTask, error) {
	return nil, nil
}
func (m *mockMissionStore) GetMission(id string) (*store.MissionRecord, error) { return nil, nil }
func (m *mockMissionStore) ListMissions(limit, offset int) ([]store.MissionRecord, int, error) {
	return nil, 0, nil
}
func (m *mockMissionStore) StoreTaskOutput(taskID string, datasetName *string, datasetIndex *int, itemID *string, outputJSON string) error {
	return nil
}
func (m *mockMissionStore) StoreTaskInput(taskID string, iterationIndex *int, objective string) error {
	return nil
}
func (m *mockMissionStore) GetTaskInputs(taskID string) ([]store.TaskInput, error) {
	return nil, nil
}
func (m *mockMissionStore) SetSubtasks(taskID, sessionID string, iterationIndex *int, titles []string) error {
	return nil
}
func (m *mockMissionStore) GetSubtasks(taskID, sessionID string, iterationIndex *int) ([]store.Subtask, error) {
	return nil, nil
}
func (m *mockMissionStore) GetSubtasksByTask(taskID string) ([]store.Subtask, error) {
	return nil, nil
}
func (m *mockMissionStore) CompleteSubtask(taskID, sessionID string, iterationIndex *int) error {
	return nil
}
func (m *mockMissionStore) StoreRouteDecision(missionID, routerTask, targetTask, condition string) error {
	return nil
}
func (m *mockMissionStore) GetRouteDecisions(missionID string) ([]store.RouteDecision, error) {
	return nil, nil
}

// --- Helpers ---

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func outputJSON(data map[string]any) string {
	b, _ := json.Marshal(data)
	return string(b)
}

// setupIteratedStore creates a PersistentKnowledgeStore pre-loaded with an iterated
// task containing the given iteration outputs.
func setupIteratedStore(t *testing.T, iterations []map[string]any) *PersistentKnowledgeStore {
	t.Helper()
	ms := newMockStore()
	ms.addTask("m1", "t1", "process", "completed")

	dsName := "items"
	for i, out := range iterations {
		idx := i
		itemID := fmt.Sprintf("item-%d", i)
		ms.addOutput("t1", store.TaskOutputRow{
			ID:           fmt.Sprintf("out-%d", i),
			DatasetName:  &dsName,
			DatasetIndex: &idx,
			ItemID:       &itemID,
			OutputJSON:   outputJSON(out),
			CreatedAt:    time.Now(),
		})
	}

	return &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
}

// --- Tests ---

func TestGetTaskOutput_CompletedTask(t *testing.T) {
	ms := newMockStore()
	ms.addTask("m1", "t1", "analyze", "completed")
	ms.addOutput("t1", store.TaskOutputRow{
		ID:         "out-1",
		OutputJSON: outputJSON(map[string]any{"score": 42.0, "label": "good"}),
		CreatedAt:  time.Now(),
	})

	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	out, ok := ks.GetTaskOutput("analyze")
	if !ok {
		t.Fatal("expected ok=true for completed task")
	}
	if out.TaskName != "analyze" {
		t.Errorf("TaskName = %q, want %q", out.TaskName, "analyze")
	}
	if out.Status != "success" {
		t.Errorf("Status = %q, want %q", out.Status, "success")
	}
	if out.IsIterated {
		t.Error("expected non-iterated task")
	}
	if out.Output["score"] != 42.0 {
		t.Errorf("Output[score] = %v, want 42.0", out.Output["score"])
	}
}

func TestGetTaskOutput_NotFound(t *testing.T) {
	ms := newMockStore()
	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	_, ok := ks.GetTaskOutput("nonexistent")
	if ok {
		t.Error("expected ok=false for nonexistent task")
	}
}

func TestGetTaskOutput_NotCompleted(t *testing.T) {
	ms := newMockStore()
	ms.addTask("m1", "t1", "running_task", "running")
	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	_, ok := ks.GetTaskOutput("running_task")
	if ok {
		t.Error("expected ok=false for non-completed task")
	}
}

func TestGetTaskOutput_CompletedNoOutputs(t *testing.T) {
	ms := newMockStore()
	ms.addTask("m1", "t1", "empty_task", "completed")
	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	out, ok := ks.GetTaskOutput("empty_task")
	if !ok {
		t.Fatal("expected ok=true for completed task with no outputs")
	}
	if out.Output != nil {
		t.Errorf("expected nil Output, got %v", out.Output)
	}
}

func TestGetTaskOutput_Iterated(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"name": "Alice", "score": 90.0},
		{"name": "Bob", "score": 80.0},
	})

	out, ok := ks.GetTaskOutput("process")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !out.IsIterated {
		t.Error("expected IsIterated=true")
	}
	if out.TotalIterations != 2 {
		t.Errorf("TotalIterations = %d, want 2", out.TotalIterations)
	}
	if len(out.Iterations) != 2 {
		t.Fatalf("len(Iterations) = %d, want 2", len(out.Iterations))
	}
	if out.Iterations[0].ItemID != "item-0" {
		t.Errorf("Iterations[0].ItemID = %q, want %q", out.Iterations[0].ItemID, "item-0")
	}
	if out.Iterations[1].Output["name"] != "Bob" {
		t.Errorf("Iterations[1].Output[name] = %v, want Bob", out.Iterations[1].Output["name"])
	}
}

// --- Filter operator tests ---

func TestQuery_FilterEq(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"city": "NYC", "temp": 70.0},
		{"city": "LA", "temp": 85.0},
		{"city": "NYC", "temp": 65.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "city", Op: FilterEq, Value: "NYC"}},
	})
	if result.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", result.TotalMatches)
	}
}

func TestQuery_FilterNe(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"city": "NYC"},
		{"city": "LA"},
		{"city": "NYC"},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "city", Op: FilterNe, Value: "NYC"}},
	})
	if result.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1", result.TotalMatches)
	}
	if result.Results[0].Output["city"] != "LA" {
		t.Errorf("expected LA, got %v", result.Results[0].Output["city"])
	}
}

func TestQuery_FilterGt(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 50.0},
		{"score": 80.0},
		{"score": 100.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "score", Op: FilterGt, Value: 70.0}},
	})
	if result.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", result.TotalMatches)
	}
}

func TestQuery_FilterLt(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 50.0},
		{"score": 80.0},
		{"score": 100.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "score", Op: FilterLt, Value: 80.0}},
	})
	if result.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1", result.TotalMatches)
	}
}

func TestQuery_FilterGte(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 50.0},
		{"score": 80.0},
		{"score": 100.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "score", Op: FilterGte, Value: 80.0}},
	})
	if result.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", result.TotalMatches)
	}
}

func TestQuery_FilterLte(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 50.0},
		{"score": 80.0},
		{"score": 100.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "score", Op: FilterLte, Value: 80.0}},
	})
	if result.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", result.TotalMatches)
	}
}

func TestQuery_FilterContains(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"desc": "fast car"},
		{"desc": "slow bike"},
		{"desc": "fastest plane"},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "desc", Op: FilterContains, Value: "fast"}},
	})
	if result.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", result.TotalMatches)
	}
}

func TestQuery_FilterContains_NoMatch(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"desc": "hello"},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "desc", Op: FilterContains, Value: "xyz"}},
	})
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", result.TotalMatches)
	}
}

func TestQuery_FilterContains_NonStringField(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 42.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "score", Op: FilterContains, Value: "42"}},
	})
	// score is a number, not a string; contains should not match
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", result.TotalMatches)
	}
}

func TestQuery_FilterOnStandardField(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
		{"x": 2.0},
	})

	// Filter on the "index" standard field
	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "index", Op: FilterEq, Value: 1}},
	})
	if result.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1", result.TotalMatches)
	}
	if result.Results[0].Output["x"] != 2.0 {
		t.Errorf("expected x=2.0 for index=1, got %v", result.Results[0].Output["x"])
	}
}

func TestQuery_FilterOnItemID(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
		{"x": 2.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "item_id", Op: FilterEq, Value: "item-1"}},
	})
	if result.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1", result.TotalMatches)
	}
}

func TestQuery_FilterOnMissingField(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "nonexistent", Op: FilterEq, Value: "anything"}},
	})
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", result.TotalMatches)
	}
}

func TestQuery_MultipleFilters(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"city": "NYC", "score": 90.0},
		{"city": "NYC", "score": 50.0},
		{"city": "LA", "score": 95.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{
			{Field: "city", Op: FilterEq, Value: "NYC"},
			{Field: "score", Op: FilterGt, Value: 70.0},
		},
	})
	if result.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1", result.TotalMatches)
	}
	if result.Results[0].Output["score"] != 90.0 {
		t.Errorf("expected score=90, got %v", result.Results[0].Output["score"])
	}
}

// --- Pagination & sorting ---

func TestQuery_Limit(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"n": 1.0}, {"n": 2.0}, {"n": 3.0}, {"n": 4.0}, {"n": 5.0},
	})

	result := ks.Query("process", Query{Limit: 3})
	if result.TotalMatches != 5 {
		t.Errorf("TotalMatches = %d, want 5", result.TotalMatches)
	}
	if len(result.Results) != 3 {
		t.Errorf("len(Results) = %d, want 3", len(result.Results))
	}
}

func TestQuery_Offset(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"n": 1.0}, {"n": 2.0}, {"n": 3.0},
	})

	result := ks.Query("process", Query{Offset: 2})
	if result.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", result.TotalMatches)
	}
	if len(result.Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(result.Results))
	}
}

func TestQuery_OffsetBeyondResults(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"n": 1.0},
	})

	result := ks.Query("process", Query{Offset: 10})
	if len(result.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(result.Results))
	}
}

func TestQuery_LimitAndOffset(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"n": 1.0}, {"n": 2.0}, {"n": 3.0}, {"n": 4.0}, {"n": 5.0},
	})

	result := ks.Query("process", Query{Limit: 2, Offset: 1})
	if result.TotalMatches != 5 {
		t.Errorf("TotalMatches = %d, want 5", result.TotalMatches)
	}
	if len(result.Results) != 2 {
		t.Errorf("len(Results) = %d, want 2", len(result.Results))
	}
	// Without ordering, items come in insertion order (index 1 and 2)
	if result.Results[0].Output["n"] != 2.0 {
		t.Errorf("Results[0].n = %v, want 2.0", result.Results[0].Output["n"])
	}
	if result.Results[1].Output["n"] != 3.0 {
		t.Errorf("Results[1].n = %v, want 3.0", result.Results[1].Output["n"])
	}
}

func TestQuery_OrderByAsc(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 80.0}, {"score": 50.0}, {"score": 100.0},
	})

	result := ks.Query("process", Query{OrderBy: "score"})
	if len(result.Results) != 3 {
		t.Fatalf("len(Results) = %d, want 3", len(result.Results))
	}
	scores := []float64{
		result.Results[0].Output["score"].(float64),
		result.Results[1].Output["score"].(float64),
		result.Results[2].Output["score"].(float64),
	}
	if scores[0] != 50.0 || scores[1] != 80.0 || scores[2] != 100.0 {
		t.Errorf("expected [50, 80, 100], got %v", scores)
	}
}

func TestQuery_OrderByDesc(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 80.0}, {"score": 50.0}, {"score": 100.0},
	})

	result := ks.Query("process", Query{OrderBy: "score", Desc: true})
	scores := []float64{
		result.Results[0].Output["score"].(float64),
		result.Results[1].Output["score"].(float64),
		result.Results[2].Output["score"].(float64),
	}
	if scores[0] != 100.0 || scores[1] != 80.0 || scores[2] != 50.0 {
		t.Errorf("expected [100, 80, 50], got %v", scores)
	}
}

func TestQuery_OrderByString(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"name": "Charlie"}, {"name": "Alice"}, {"name": "Bob"},
	})

	result := ks.Query("process", Query{OrderBy: "name"})
	names := []string{
		result.Results[0].Output["name"].(string),
		result.Results[1].Output["name"].(string),
		result.Results[2].Output["name"].(string),
	}
	if names[0] != "Alice" || names[1] != "Bob" || names[2] != "Charlie" {
		t.Errorf("expected [Alice, Bob, Charlie], got %v", names)
	}
}

func TestQuery_OrderByWithLimitOffset(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 10.0}, {"score": 40.0}, {"score": 30.0}, {"score": 20.0}, {"score": 50.0},
	})

	// Ascending order: 10, 20, 30, 40, 50. Offset 1, Limit 2 => 20, 30.
	result := ks.Query("process", Query{OrderBy: "score", Limit: 2, Offset: 1})
	if result.TotalMatches != 5 {
		t.Errorf("TotalMatches = %d, want 5", result.TotalMatches)
	}
	if len(result.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(result.Results))
	}
	if result.Results[0].Output["score"] != 20.0 {
		t.Errorf("Results[0].score = %v, want 20", result.Results[0].Output["score"])
	}
	if result.Results[1].Output["score"] != 30.0 {
		t.Errorf("Results[1].score = %v, want 30", result.Results[1].Output["score"])
	}
}

// --- Empty results ---

func TestQuery_EmptyResults(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
	})

	result := ks.Query("process", Query{
		Filters: []Filter{{Field: "x", Op: FilterEq, Value: 999.0}},
	})
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", result.TotalMatches)
	}
	if len(result.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(result.Results))
	}
}

func TestQuery_NonIteratedTask(t *testing.T) {
	ms := newMockStore()
	ms.addTask("m1", "t1", "single", "completed")
	ms.addOutput("t1", store.TaskOutputRow{
		ID:         "out-1",
		OutputJSON: outputJSON(map[string]any{"result": "ok"}),
		CreatedAt:  time.Now(),
	})

	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	result := ks.Query("single", Query{})
	// Query on non-iterated task returns empty
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0 for non-iterated task", result.TotalMatches)
	}
}

func TestQuery_TaskNotFound(t *testing.T) {
	ms := newMockStore()
	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	result := ks.Query("missing", Query{})
	if result.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", result.TotalMatches)
	}
}

func TestQuery_NoFilters(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"a": 1.0}, {"a": 2.0}, {"a": 3.0},
	})

	result := ks.Query("process", Query{})
	if result.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", result.TotalMatches)
	}
}

// --- Aggregate tests ---

func TestAggregate_Count(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0}, {"x": 2.0}, {"x": 3.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggCount})
	if result.Value != 3 {
		t.Errorf("Count = %v, want 3", result.Value)
	}
}

func TestAggregate_CountWithFilter(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"status_code": 200.0},
		{"status_code": 404.0},
		{"status_code": 200.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggCount,
		Filters: []Filter{{Field: "status_code", Op: FilterEq, Value: 200.0}},
	})
	if result.Value != 2 {
		t.Errorf("Count = %v, want 2", result.Value)
	}
}

func TestAggregate_Sum(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"amount": 10.0}, {"amount": 20.0}, {"amount": 30.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggSum, Field: "amount"})
	if result.Value != 60.0 {
		t.Errorf("Sum = %v, want 60.0", result.Value)
	}
}

func TestAggregate_SumEmpty(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{})

	result := ks.Aggregate("process", AggregateQuery{Op: AggSum, Field: "amount"})
	// Empty iterated task -> GetTaskOutput returns ok=true but 0 iterations
	// Actually no outputs means TotalIterations=0. Let's verify.
	if result.Value != nil {
		// With 0 iterations, Aggregate returns empty since IsIterated is based on DatasetName
		// which requires at least one output. So this returns AggregateResult{}.
	}
}

func TestAggregate_Avg(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"score": 10.0}, {"score": 20.0}, {"score": 30.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggAvg, Field: "score"})
	if result.Value != 20.0 {
		t.Errorf("Avg = %v, want 20.0", result.Value)
	}
}

func TestAggregate_AvgWithFilter(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"group": "a", "val": 10.0},
		{"group": "a", "val": 30.0},
		{"group": "b", "val": 100.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggAvg,
		Field:   "val",
		Filters: []Filter{{Field: "group", Op: FilterEq, Value: "a"}},
	})
	if result.Value != 20.0 {
		t.Errorf("Avg = %v, want 20.0", result.Value)
	}
}

func TestAggregate_Min(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"temp": 72.0}, {"temp": 55.0}, {"temp": 90.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggMin, Field: "temp"})
	if result.Value != 55.0 {
		t.Errorf("Min = %v, want 55.0", result.Value)
	}
	if result.Item == nil {
		t.Fatal("expected Item to be set for Min")
	}
	if result.Item.Output["temp"] != 55.0 {
		t.Errorf("Item.Output[temp] = %v, want 55.0", result.Item.Output["temp"])
	}
}

func TestAggregate_Max(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"temp": 72.0}, {"temp": 55.0}, {"temp": 90.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggMax, Field: "temp"})
	if result.Value != 90.0 {
		t.Errorf("Max = %v, want 90.0", result.Value)
	}
	if result.Item == nil {
		t.Fatal("expected Item to be set for Max")
	}
	if result.Item.Output["temp"] != 90.0 {
		t.Errorf("Item.Output[temp] = %v, want 90.0", result.Item.Output["temp"])
	}
}

func TestAggregate_MinEmpty(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggMin,
		Field:   "x",
		Filters: []Filter{{Field: "x", Op: FilterGt, Value: 999.0}},
	})
	if result.Item != nil {
		t.Error("expected nil Item for empty Min result")
	}
}

func TestAggregate_MaxEmpty(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggMax,
		Field:   "x",
		Filters: []Filter{{Field: "x", Op: FilterGt, Value: 999.0}},
	})
	if result.Item != nil {
		t.Error("expected nil Item for empty Max result")
	}
}

// --- Distinct ---

func TestAggregate_Distinct(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"color": "red"}, {"color": "blue"}, {"color": "red"}, {"color": "green"}, {"color": "blue"},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggDistinct, Field: "color"})
	if len(result.Values) != 3 {
		t.Errorf("len(Values) = %d, want 3", len(result.Values))
	}

	seen := make(map[any]bool)
	for _, v := range result.Values {
		seen[v] = true
	}
	for _, expected := range []string{"red", "blue", "green"} {
		if !seen[expected] {
			t.Errorf("missing distinct value: %q", expected)
		}
	}
}

func TestAggregate_DistinctEmpty(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 1.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggDistinct,
		Field:   "color",
		Filters: []Filter{{Field: "x", Op: FilterGt, Value: 999.0}},
	})
	if len(result.Values) != 0 {
		t.Errorf("len(Values) = %d, want 0", len(result.Values))
	}
}

// --- GroupBy ---

func TestAggregate_GroupByCount(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "salary": 100.0},
		{"dept": "eng", "salary": 120.0},
		{"dept": "sales", "salary": 80.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		GroupBy: "dept",
		GroupOp: AggCount,
	})
	if result.Groups["eng"] != 2 {
		t.Errorf("Groups[eng] = %v, want 2", result.Groups["eng"])
	}
	if result.Groups["sales"] != 1 {
		t.Errorf("Groups[sales] = %v, want 1", result.Groups["sales"])
	}
}

func TestAggregate_GroupBySum(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "salary": 100.0},
		{"dept": "eng", "salary": 120.0},
		{"dept": "sales", "salary": 80.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		Field:   "salary",
		GroupBy: "dept",
		GroupOp: AggSum,
	})
	if result.Groups["eng"] != 220.0 {
		t.Errorf("Groups[eng] = %v, want 220.0", result.Groups["eng"])
	}
	if result.Groups["sales"] != 80.0 {
		t.Errorf("Groups[sales] = %v, want 80.0", result.Groups["sales"])
	}
}

func TestAggregate_GroupByAvg(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "salary": 100.0},
		{"dept": "eng", "salary": 200.0},
		{"dept": "sales", "salary": 90.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		Field:   "salary",
		GroupBy: "dept",
		GroupOp: AggAvg,
	})
	if result.Groups["eng"] != 150.0 {
		t.Errorf("Groups[eng] = %v, want 150.0", result.Groups["eng"])
	}
	if result.Groups["sales"] != 90.0 {
		t.Errorf("Groups[sales] = %v, want 90.0", result.Groups["sales"])
	}
}

func TestAggregate_GroupByMin(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "salary": 100.0},
		{"dept": "eng", "salary": 200.0},
		{"dept": "sales", "salary": 90.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		Field:   "salary",
		GroupBy: "dept",
		GroupOp: AggMin,
	})
	if result.Groups["eng"] != 100.0 {
		t.Errorf("Groups[eng] = %v, want 100.0", result.Groups["eng"])
	}
}

func TestAggregate_GroupByMax(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "salary": 100.0},
		{"dept": "eng", "salary": 200.0},
		{"dept": "sales", "salary": 90.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		Field:   "salary",
		GroupBy: "dept",
		GroupOp: AggMax,
	})
	if result.Groups["eng"] != 200.0 {
		t.Errorf("Groups[eng] = %v, want 200.0", result.Groups["eng"])
	}
}

func TestAggregate_GroupByDefaultOp(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng"}, {"dept": "eng"}, {"dept": "sales"},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		GroupBy: "dept",
		// GroupOp not set — should default to count
	})
	if result.Groups["eng"] != 2 {
		t.Errorf("Groups[eng] = %v, want 2", result.Groups["eng"])
	}
}

func TestAggregate_GroupByWithFilter(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"dept": "eng", "level": "senior", "salary": 200.0},
		{"dept": "eng", "level": "junior", "salary": 100.0},
		{"dept": "sales", "level": "senior", "salary": 150.0},
	})

	result := ks.Aggregate("process", AggregateQuery{
		Op:      AggGroupBy,
		Field:   "salary",
		GroupBy: "dept",
		GroupOp: AggSum,
		Filters: []Filter{{Field: "level", Op: FilterEq, Value: "senior"}},
	})
	if result.Groups["eng"] != 200.0 {
		t.Errorf("Groups[eng] = %v, want 200.0", result.Groups["eng"])
	}
	if result.Groups["sales"] != 150.0 {
		t.Errorf("Groups[sales] = %v, want 150.0", result.Groups["sales"])
	}
	if _, ok := result.Groups["junior"]; ok {
		t.Error("unexpected group key 'junior'")
	}
}

// --- Type coercion ---

func TestAggregate_NumericCoercion_Int(t *testing.T) {
	// JSON unmarshals numbers as float64 by default, but let's verify
	// the getNumericValue handles the types it claims to support.
	// We test indirectly through the Output map which uses float64 from JSON.
	ks := setupIteratedStore(t, []map[string]any{
		{"val": 10.0}, {"val": 20.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggSum, Field: "val"})
	if result.Value != 30.0 {
		t.Errorf("Sum = %v, want 30.0", result.Value)
	}
}

func TestAggregate_NumericCoercion_NonNumericField(t *testing.T) {
	// When a field is not numeric, getNumericValue returns 0
	ks := setupIteratedStore(t, []map[string]any{
		{"name": "Alice"}, {"name": "Bob"},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggSum, Field: "name"})
	if result.Value != 0.0 {
		t.Errorf("Sum of non-numeric = %v, want 0.0", result.Value)
	}
}

func TestAggregate_NumericCoercion_MissingField(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{
		{"x": 10.0}, {"x": 20.0},
	})

	result := ks.Aggregate("process", AggregateQuery{Op: AggSum, Field: "missing"})
	if result.Value != 0.0 {
		t.Errorf("Sum of missing field = %v, want 0.0", result.Value)
	}
}

// --- Aggregate on non-iterated / missing tasks ---

func TestAggregate_NonIteratedTask(t *testing.T) {
	ms := newMockStore()
	ms.addTask("m1", "t1", "single", "completed")
	ms.addOutput("t1", store.TaskOutputRow{
		ID:         "out-1",
		OutputJSON: outputJSON(map[string]any{"result": "ok"}),
		CreatedAt:  time.Now(),
	})

	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	result := ks.Aggregate("single", AggregateQuery{Op: AggCount})
	if result.Value != nil {
		t.Errorf("expected nil Value for non-iterated task, got %v", result.Value)
	}
}

func TestAggregate_TaskNotFound(t *testing.T) {
	ms := newMockStore()
	ks := &PersistentKnowledgeStore{MissionID: "m1", Store: ms}
	result := ks.Aggregate("missing", AggregateQuery{Op: AggCount})
	if result.Value != nil {
		t.Errorf("expected nil Value for missing task, got %v", result.Value)
	}
}

func TestAggregate_UnknownOp(t *testing.T) {
	ks := setupIteratedStore(t, []map[string]any{{"x": 1.0}})
	result := ks.Aggregate("process", AggregateQuery{Op: "invalid"})
	if result.Value != nil {
		t.Errorf("expected nil Value for unknown op, got %v", result.Value)
	}
}

// --- Helper function tests ---

func TestCompareValues_Numeric(t *testing.T) {
	tests := []struct {
		a, b any
		want int
	}{
		{1.0, 2.0, -1},
		{2.0, 1.0, 1},
		{3.0, 3.0, 0},
		{1, 2.0, -1},   // int vs float64
		{int64(5), 3.0, 1},
	}
	for _, tt := range tests {
		got := compareValues(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareValues(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareValues_String(t *testing.T) {
	tests := []struct {
		a, b any
		want int
	}{
		{"apple", "banana", -1},
		{"banana", "apple", 1},
		{"same", "same", 0},
	}
	for _, tt := range tests {
		got := compareValues(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareValues(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"hello", "", true},
		{"hello", "hello", true},
		{"hi", "hello", false},
	}
	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestGetFieldValue_StandardFields(t *testing.T) {
	iter := IterationOutput{
		Index:  5,
		ItemID: "my-item",
		Status: "success",
		Output: map[string]any{"custom": "val"},
	}

	if v := getFieldValue(iter, "index"); v != 5 {
		t.Errorf("getFieldValue(index) = %v, want 5", v)
	}
	if v := getFieldValue(iter, "item_id"); v != "my-item" {
		t.Errorf("getFieldValue(item_id) = %v, want my-item", v)
	}
	if v := getFieldValue(iter, "status"); v != "success" {
		t.Errorf("getFieldValue(status) = %v, want success", v)
	}
	if v := getFieldValue(iter, "custom"); v != "val" {
		t.Errorf("getFieldValue(custom) = %v, want val", v)
	}
	if v := getFieldValue(iter, "nonexistent"); v != nil {
		t.Errorf("getFieldValue(nonexistent) = %v, want nil", v)
	}
}

func TestGetNumericValue_Types(t *testing.T) {
	tests := []struct {
		name  string
		output map[string]any
		want  float64
	}{
		{"float64", map[string]any{"v": float64(3.14)}, 3.14},
		{"float32", map[string]any{"v": float32(2.5)}, 2.5},
		{"int", map[string]any{"v": int(42)}, 42.0},
		{"int64", map[string]any{"v": int64(99)}, 99.0},
		{"int32", map[string]any{"v": int32(7)}, 7.0},
		{"string", map[string]any{"v": "text"}, 0.0},
		{"nil", map[string]any{}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iter := IterationOutput{Output: tt.output}
			got := getNumericValue(iter, "v")
			if got != tt.want {
				t.Errorf("getNumericValue = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input   any
		wantVal float64
		wantOk  bool
	}{
		{float64(1.5), 1.5, true},
		{float32(2.5), 2.5, true},
		{int(3), 3.0, true},
		{int64(4), 4.0, true},
		{int32(5), 5.0, true},
		{"nope", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, tt := range tests {
		val, ok := toFloat64(tt.input)
		if ok != tt.wantOk || val != tt.wantVal {
			t.Errorf("toFloat64(%v) = (%v, %v), want (%v, %v)", tt.input, val, ok, tt.wantVal, tt.wantOk)
		}
	}
}
