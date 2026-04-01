package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// =============================================================================
// ResultInterceptor tests
// =============================================================================

func TestInterceptSmallResultPassesThrough(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	result := interceptor.Intercept("my_tool", "small result")

	if result.Data != "small result" {
		t.Errorf("expected data to be 'small result', got %q", result.Data)
	}
	if result.Metadata != "" {
		t.Errorf("expected empty metadata, got %q", result.Metadata)
	}
	if result.ID != "" {
		t.Errorf("expected empty ID, got %q", result.ID)
	}
}

func TestInterceptSmallJSONPassesThrough(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	smallJSON := `{"key": "value"}`
	result := interceptor.Intercept("my_tool", smallJSON)

	if result.Data != smallJSON {
		t.Errorf("expected data to pass through unchanged, got %q", result.Data)
	}
	if result.ID != "" {
		t.Errorf("expected no interception, got ID %q", result.ID)
	}
}

func TestInterceptLargeTextResult(t *testing.T) {
	store := NewMemoryResultStore()
	config := DefaultLargeResultConfig()
	interceptor := NewResultInterceptor(store, config)

	// Create a string larger than 8KB
	largeText := strings.Repeat("x", 10000)
	result := interceptor.Intercept("my_tool", largeText)

	if result.ID == "" {
		t.Fatal("expected result to be intercepted with an ID")
	}
	if result.Metadata == "" {
		t.Fatal("expected metadata to be set")
	}
	if !strings.Contains(result.Metadata, "type: text") {
		t.Errorf("expected metadata to contain 'type: text', got %q", result.Metadata)
	}
	if !strings.Contains(result.Metadata, "partial: true") {
		t.Errorf("expected metadata to contain 'partial: true', got %q", result.Metadata)
	}
	if !strings.Contains(result.Metadata, "total_bytes: 10000") {
		t.Errorf("expected metadata to contain 'total_bytes: 10000', got %q", result.Metadata)
	}
	// Data should be a preview (500 chars + "...")
	if len(result.Data) != config.PreviewLength+3 { // 500 + "..."
		t.Errorf("expected preview length %d, got %d", config.PreviewLength+3, len(result.Data))
	}
	if !strings.HasSuffix(result.Data, "...") {
		t.Error("expected preview to end with '...'")
	}
}

func TestInterceptLargeJSONArray(t *testing.T) {
	store := NewMemoryResultStore()
	config := DefaultLargeResultConfig()
	interceptor := NewResultInterceptor(store, config)

	// Create array with more than 20 items
	items := make([]map[string]string, 25)
	for i := 0; i < 25; i++ {
		items[i] = map[string]string{"id": fmt.Sprintf("item_%d", i)}
	}
	data, _ := json.Marshal(items)

	result := interceptor.Intercept("my_tool", string(data))

	if result.ID == "" {
		t.Fatal("expected result to be intercepted")
	}
	if !strings.Contains(result.Metadata, "type: array") {
		t.Errorf("expected 'type: array' in metadata, got %q", result.Metadata)
	}
	if !strings.Contains(result.Metadata, "total_items: 25") {
		t.Errorf("expected 'total_items: 25' in metadata, got %q", result.Metadata)
	}
	if !strings.Contains(result.Metadata, "shown_items: 5") {
		t.Errorf("expected 'shown_items: 5' in metadata, got %q", result.Metadata)
	}

	// Data should be a JSON array of 5 sample items
	var sample []any
	if err := json.Unmarshal([]byte(result.Data), &sample); err != nil {
		t.Fatalf("expected sample data to be valid JSON array: %v", err)
	}
	if len(sample) != 5 {
		t.Errorf("expected 5 sample items, got %d", len(sample))
	}
}

func TestInterceptLargeJSONObject(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	// Create a large JSON object (>8KB)
	obj := make(map[string]string)
	for i := 0; i < 100; i++ {
		obj[fmt.Sprintf("key_%03d", i)] = strings.Repeat("v", 100)
	}
	data, _ := json.Marshal(obj)

	result := interceptor.Intercept("my_tool", string(data))

	if result.ID == "" {
		t.Fatal("expected result to be intercepted")
	}
	if !strings.Contains(result.Metadata, "type: object") {
		t.Errorf("expected 'type: object' in metadata, got %q", result.Metadata)
	}
	if !strings.Contains(result.Metadata, "total_keys: 100") {
		t.Errorf("expected 'total_keys: 100' in metadata, got %q", result.Metadata)
	}
	if !strings.Contains(result.Data, "Top-level keys:") {
		t.Errorf("expected data to contain key listing, got %q", result.Data)
	}
}

func TestInterceptResultToolsNotReIntercepted(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	largeText := strings.Repeat("x", 10000)

	// Tools starting with "result_" should never be intercepted
	for _, toolName := range []string{"result_items", "result_get", "result_full"} {
		result := interceptor.Intercept(toolName, largeText)
		if result.ID != "" {
			t.Errorf("tool %q should not be intercepted, got ID %q", toolName, result.ID)
		}
		if result.Data != largeText {
			t.Errorf("tool %q should return full data unchanged", toolName)
		}
		if result.Metadata != "" {
			t.Errorf("tool %q should have empty metadata", toolName)
		}
	}
}

func TestInterceptNilStorePassesThrough(t *testing.T) {
	interceptor := NewResultInterceptor(nil, DefaultLargeResultConfig())

	largeText := strings.Repeat("x", 10000)
	result := interceptor.Intercept("my_tool", largeText)

	if result.Data != largeText {
		t.Error("expected data to pass through when store is nil")
	}
	if result.ID != "" {
		t.Error("expected no interception when store is nil")
	}
}

func TestInterceptArrayBelowItemThresholdButLargeBytes(t *testing.T) {
	store := NewMemoryResultStore()
	config := DefaultLargeResultConfig()
	interceptor := NewResultInterceptor(store, config)

	// 10 items (below threshold of 20) but each item is large enough to exceed byte threshold
	items := make([]map[string]string, 10)
	for i := 0; i < 10; i++ {
		items[i] = map[string]string{"data": strings.Repeat("x", 1000)}
	}
	data, _ := json.Marshal(items)

	result := interceptor.Intercept("my_tool", string(data))

	// Should NOT be intercepted as array (below item threshold)
	// but might be intercepted as text/object if over byte threshold
	// Since it's a valid JSON array but under item threshold, and over byte threshold,
	// it won't match array interception. It will try object (fail - it's an array),
	// then fall through to text interception since len > 8KB.
	if len(data) > config.ByteThreshold {
		// Should be intercepted as text since it's not an array (below threshold) and not an object
		if result.ID == "" {
			t.Error("expected large byte result to be intercepted")
		}
	}
}

// =============================================================================
// MemoryResultStore tests
// =============================================================================

func TestMemoryResultStoreStoreAndGet(t *testing.T) {
	store := NewMemoryResultStore()

	stored := StoredResult{
		Type:    ResultTypeText,
		Size:    100,
		RawData: "hello world",
	}
	id := store.Store("test_tool", stored)

	if id == "" {
		t.Fatal("expected non-empty ID")
	}
	if !strings.HasPrefix(id, "_result_test_tool_") {
		t.Errorf("expected ID to start with '_result_test_tool_', got %q", id)
	}

	retrieved, ok := store.Get(id)
	if !ok {
		t.Fatal("expected to find stored result")
	}
	if retrieved.RawData != "hello world" {
		t.Errorf("expected RawData 'hello world', got %q", retrieved.RawData)
	}
	if retrieved.ToolName != "test_tool" {
		t.Errorf("expected ToolName 'test_tool', got %q", retrieved.ToolName)
	}
	if retrieved.ID != id {
		t.Errorf("expected ID %q, got %q", id, retrieved.ID)
	}
}

func TestMemoryResultStoreGetMissing(t *testing.T) {
	store := NewMemoryResultStore()

	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for missing ID")
	}
}

func TestMemoryResultStoreGetInfo(t *testing.T) {
	store := NewMemoryResultStore()

	store.Store("tool_a", StoredResult{Type: ResultTypeArray, Size: 50})
	store.Store("tool_b", StoredResult{Type: ResultTypeText, Size: 1000})

	infos := store.GetInfo()
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}

	// Check that both results are represented (order is non-deterministic from map)
	types := map[ResultType]bool{}
	for _, info := range infos {
		types[info.Type] = true
		if info.ID == "" {
			t.Error("expected non-empty ID in info")
		}
	}
	if !types[ResultTypeArray] || !types[ResultTypeText] {
		t.Error("expected both array and text types in infos")
	}
}

func TestMemoryResultStoreUniqueIDs(t *testing.T) {
	store := NewMemoryResultStore()

	id1 := store.Store("tool", StoredResult{Type: ResultTypeText})
	id2 := store.Store("tool", StoredResult{Type: ResultTypeText})

	if id1 == id2 {
		t.Errorf("expected unique IDs, both are %q", id1)
	}
}

func TestMemoryResultStoreSanitizesName(t *testing.T) {
	store := NewMemoryResultStore()

	id := store.Store("my-tool.v2", StoredResult{Type: ResultTypeText})

	if strings.Contains(id, "-") || strings.Contains(id, ".") {
		t.Errorf("expected sanitized ID (no dots or hyphens), got %q", id)
	}
	if !strings.Contains(id, "my_tool_v2") {
		t.Errorf("expected 'my_tool_v2' in ID, got %q", id)
	}
}

// =============================================================================
// SubmitOutputTool tests
// =============================================================================

func TestSubmitOutputToolSchema(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	if tool.ToolName() != "submit_output" {
		t.Errorf("expected tool name 'submit_output', got %q", tool.ToolName())
	}

	schema := tool.ToolPayloadSchema()
	if schema.Type != TypeObject {
		t.Errorf("expected schema type 'object', got %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "output" {
		t.Errorf("expected required field 'output', got %v", schema.Required)
	}
	if _, ok := schema.Properties["output"]; !ok {
		t.Error("expected 'output' property in schema")
	}
}

func TestSubmitOutputToolSuccessfulSubmission(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	result := tool.Call(context.Background(), `{"output": {"summary": "test result", "score": 42}}`)

	if !strings.Contains(result, `"status": "ok"`) {
		t.Errorf("expected success status, got %q", result)
	}
	if !strings.Contains(result, `"index": 0`) {
		t.Errorf("expected index 0, got %q", result)
	}

	if tool.ResultCount() != 1 {
		t.Errorf("expected result count 1, got %d", tool.ResultCount())
	}

	results := tool.GetResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Output["summary"] != "test result" {
		t.Errorf("expected summary 'test result', got %v", results[0].Output["summary"])
	}
}

func TestSubmitOutputToolMultipleSubmissions(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	r1 := tool.Call(context.Background(), `{"output": {"item": "first"}}`)
	r2 := tool.Call(context.Background(), `{"output": {"item": "second"}}`)

	if !strings.Contains(r1, `"index": 0`) {
		t.Errorf("expected index 0 for first submission, got %q", r1)
	}
	if !strings.Contains(r2, `"index": 1`) {
		t.Errorf("expected index 1 for second submission, got %q", r2)
	}
	if tool.ResultCount() != 2 {
		t.Errorf("expected result count 2, got %d", tool.ResultCount())
	}
}

func TestSubmitOutputToolMissingOutput(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	result := tool.Call(context.Background(), `{}`)

	if !strings.Contains(result, "error") {
		t.Errorf("expected error for missing output, got %q", result)
	}
	if !strings.Contains(result, "output is required") {
		t.Errorf("expected 'output is required' message, got %q", result)
	}
}

func TestSubmitOutputToolInvalidJSON(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	result := tool.Call(context.Background(), `not json`)

	if !strings.Contains(result, "error") {
		t.Errorf("expected error for invalid JSON, got %q", result)
	}
}

func TestSubmitOutputToolNullOutput(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	result := tool.Call(context.Background(), `{"output": null}`)

	if !strings.Contains(result, "error") {
		t.Errorf("expected error for null output, got %q", result)
	}
}

func TestSubmitOutputToolSchemaValidation(t *testing.T) {
	schema := []OutputField{
		{Name: "title", Type: "string", Required: true},
		{Name: "count", Type: "integer", Required: true},
		{Name: "notes", Type: "string", Required: false},
	}
	tool := NewSubmitOutputTool(schema)

	// Missing required fields
	result := tool.Call(context.Background(), `{"output": {"notes": "optional only"}}`)
	if !strings.Contains(result, "error") {
		t.Errorf("expected error for missing required fields, got %q", result)
	}
	if !strings.Contains(result, "title") {
		t.Errorf("expected 'title' in missing fields, got %q", result)
	}
	if !strings.Contains(result, "count") {
		t.Errorf("expected 'count' in missing fields, got %q", result)
	}

	// All required fields present (optional omitted)
	result = tool.Call(context.Background(), `{"output": {"title": "Test", "count": 5}}`)
	if !strings.Contains(result, `"status": "ok"`) {
		t.Errorf("expected success when all required fields present, got %q", result)
	}
}

func TestSubmitOutputToolCallback(t *testing.T) {
	tool := NewSubmitOutputTool(nil)

	var callbackIndex int
	var callbackOutput map[string]any
	tool.OnSubmit = func(index int, output map[string]any) {
		callbackIndex = index
		callbackOutput = output
	}

	tool.Call(context.Background(), `{"output": {"key": "value"}}`)

	if callbackIndex != 0 {
		t.Errorf("expected callback index 0, got %d", callbackIndex)
	}
	if callbackOutput["key"] != "value" {
		t.Errorf("expected callback output key 'value', got %v", callbackOutput["key"])
	}
}

// =============================================================================
// DatasetCursor tests
// =============================================================================

func TestDatasetCursorNextReturnsSequentially(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("first"),
		cty.StringVal("second"),
		cty.StringVal("third"),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	// First call
	r1 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r1, `"status": "ok"`) {
		t.Fatalf("expected ok status, got %q", r1)
	}
	if !strings.Contains(r1, `"index": 0`) {
		t.Errorf("expected index 0, got %q", r1)
	}
	if !strings.Contains(r1, `"total": 3`) {
		t.Errorf("expected total 3, got %q", r1)
	}
	if !strings.Contains(r1, `"first"`) {
		t.Errorf("expected item 'first', got %q", r1)
	}

	// Second call
	r2 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r2, `"index": 1`) {
		t.Errorf("expected index 1, got %q", r2)
	}
	if !strings.Contains(r2, `"second"`) {
		t.Errorf("expected item 'second', got %q", r2)
	}

	// Third call
	r3 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r3, `"index": 2`) {
		t.Errorf("expected index 2, got %q", r3)
	}
	if !strings.Contains(r3, `"third"`) {
		t.Errorf("expected item 'third', got %q", r3)
	}
}

func TestDatasetCursorNextReturnsExhausted(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("only"),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	// Consume the only item
	r1 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r1, `"status": "ok"`) {
		t.Fatalf("expected ok for first call, got %q", r1)
	}

	// Now exhausted
	r2 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r2, `"status": "exhausted"`) {
		t.Errorf("expected exhausted status, got %q", r2)
	}

	// Calling again should still be exhausted
	r3 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r3, `"status": "exhausted"`) {
		t.Errorf("expected exhausted on repeated call, got %q", r3)
	}
}

func TestDatasetCursorEmptyDataset(t *testing.T) {
	cursor := NewDatasetCursor("test_task", []cty.Value{})
	nextTool := NewDatasetNextTool(cursor)

	result := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(result, `"status": "exhausted"`) {
		t.Errorf("expected exhausted for empty dataset, got %q", result)
	}
}

func TestDatasetCursorTotal(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("a"),
		cty.StringVal("b"),
		cty.StringVal("c"),
	}
	cursor := NewDatasetCursor("test_task", items)

	if cursor.Total() != 3 {
		t.Errorf("expected Total() = 3, got %d", cursor.Total())
	}
}

func TestDatasetCursorCurrentIndex(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("a"),
		cty.StringVal("b"),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	// Before any call, CurrentIndex should be -1
	if cursor.CurrentIndex() != -1 {
		t.Errorf("expected CurrentIndex() = -1 before first Next, got %d", cursor.CurrentIndex())
	}

	nextTool.Call(context.Background(), `{}`)
	if cursor.CurrentIndex() != 0 {
		t.Errorf("expected CurrentIndex() = 0 after first Next, got %d", cursor.CurrentIndex())
	}

	nextTool.Call(context.Background(), `{}`)
	if cursor.CurrentIndex() != 1 {
		t.Errorf("expected CurrentIndex() = 1 after second Next, got %d", cursor.CurrentIndex())
	}
}

func TestDatasetCursorOnNextCallback(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("a"),
		cty.StringVal("b"),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	var calledIndices []int
	cursor.OnNext = func(index int) {
		calledIndices = append(calledIndices, index)
	}

	nextTool.Call(context.Background(), `{}`)
	nextTool.Call(context.Background(), `{}`)

	if len(calledIndices) != 2 {
		t.Fatalf("expected 2 OnNext callbacks, got %d", len(calledIndices))
	}
	if calledIndices[0] != 0 || calledIndices[1] != 1 {
		t.Errorf("expected callback indices [0, 1], got %v", calledIndices)
	}
}

func TestDatasetCursorOutputGating(t *testing.T) {
	items := []cty.Value{
		cty.StringVal("a"),
		cty.StringVal("b"),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	outputCount := 0
	nextTool.OutputCounter = func() int { return outputCount }

	// First call should work (no previous item to submit for)
	r1 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r1, `"status": "ok"`) {
		t.Fatalf("expected ok for first call, got %q", r1)
	}

	// Second call without submitting output should error
	r2 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r2, `"status": "error"`) {
		t.Errorf("expected error when output not submitted, got %q", r2)
	}
	if !strings.Contains(r2, "submit_output") {
		t.Errorf("expected message about submit_output, got %q", r2)
	}

	// Simulate output submission
	outputCount = 1
	r3 := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(r3, `"status": "ok"`) {
		t.Errorf("expected ok after output submitted, got %q", r3)
	}
}

func TestDatasetNextToolWithObjectItems(t *testing.T) {
	items := []cty.Value{
		cty.ObjectVal(map[string]cty.Value{
			"name":  cty.StringVal("Alice"),
			"score": cty.NumberIntVal(95),
		}),
		cty.ObjectVal(map[string]cty.Value{
			"name":  cty.StringVal("Bob"),
			"score": cty.NumberIntVal(87),
		}),
	}
	cursor := NewDatasetCursor("test_task", items)
	nextTool := NewDatasetNextTool(cursor)

	result := nextTool.Call(context.Background(), `{}`)
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected item to contain 'Alice', got %q", result)
	}
	if !strings.Contains(result, `"status": "ok"`) {
		t.Errorf("expected ok status, got %q", result)
	}
}

func TestDatasetNextToolName(t *testing.T) {
	cursor := NewDatasetCursor("test", nil)
	tool := NewDatasetNextTool(cursor)

	if tool.ToolName() != "dataset_next" {
		t.Errorf("expected tool name 'dataset_next', got %q", tool.ToolName())
	}
}

// =============================================================================
// Integration: ResultInterceptor + MemoryResultStore round-trip
// =============================================================================

func TestInterceptorStoreRoundTrip(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	original := strings.Repeat("data ", 2000) // ~10KB
	result := interceptor.Intercept("fetch_data", original)

	if result.ID == "" {
		t.Fatal("expected interception")
	}

	// Retrieve full data from store
	stored, ok := store.Get(result.ID)
	if !ok {
		t.Fatal("expected to find stored result")
	}
	if stored.RawData != original {
		t.Error("expected stored RawData to match original")
	}
	if stored.Type != ResultTypeText {
		t.Errorf("expected type text, got %q", stored.Type)
	}
	if stored.ToolName != "fetch_data" {
		t.Errorf("expected tool name 'fetch_data', got %q", stored.ToolName)
	}
}

func TestInterceptorArrayStoreRoundTrip(t *testing.T) {
	store := NewMemoryResultStore()
	interceptor := NewResultInterceptor(store, DefaultLargeResultConfig())

	items := make([]int, 30)
	for i := range items {
		items[i] = i
	}
	data, _ := json.Marshal(items)

	result := interceptor.Intercept("list_items", string(data))

	if result.ID == "" {
		t.Fatal("expected interception")
	}

	stored, ok := store.Get(result.ID)
	if !ok {
		t.Fatal("expected to find stored result")
	}
	if stored.Type != ResultTypeArray {
		t.Errorf("expected type array, got %q", stored.Type)
	}
	if stored.Size != 30 {
		t.Errorf("expected size 30, got %d", stored.Size)
	}
	if len(stored.Array) != 30 {
		t.Errorf("expected 30 array items in store, got %d", len(stored.Array))
	}
}
