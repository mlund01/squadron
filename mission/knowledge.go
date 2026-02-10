package mission

import (
	"fmt"
	"sort"
	"sync"
)

// FilterOp represents a filter operation
type FilterOp string

const (
	FilterEq       FilterOp = "eq"
	FilterNe       FilterOp = "ne"
	FilterGt       FilterOp = "gt"
	FilterLt       FilterOp = "lt"
	FilterGte      FilterOp = "gte"
	FilterLte      FilterOp = "lte"
	FilterContains FilterOp = "contains"
)

// AggregateOp represents an aggregate operation
type AggregateOp string

const (
	AggCount    AggregateOp = "count"
	AggSum      AggregateOp = "sum"
	AggAvg      AggregateOp = "avg"
	AggMin      AggregateOp = "min"
	AggMax      AggregateOp = "max"
	AggDistinct AggregateOp = "distinct"
	AggGroupBy  AggregateOp = "group_by"
)

// Filter represents a single filter condition
type Filter struct {
	Field string   `json:"field"`
	Op    FilterOp `json:"op"`
	Value any      `json:"value"`
}

// Query represents a query for task outputs
type Query struct {
	Filters []Filter `json:"filters,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Offset  int      `json:"offset,omitempty"`
	OrderBy string   `json:"order_by,omitempty"`
	Desc    bool     `json:"desc,omitempty"`
}

// QueryResult represents the result of a query
type QueryResult struct {
	TotalMatches int               `json:"total_matches"`
	Results      []IterationOutput `json:"results"`
}

// AggregateQuery represents an aggregate query
type AggregateQuery struct {
	Op      AggregateOp `json:"op"`
	Field   string      `json:"field,omitempty"`
	Filters []Filter    `json:"filters,omitempty"`
	GroupBy string      `json:"group_by,omitempty"` // For group_by operation
	GroupOp AggregateOp `json:"group_op,omitempty"` // Operation within groups
}

// AggregateResult represents the result of an aggregate query
type AggregateResult struct {
	Value  any                `json:"value,omitempty"`  // For count/sum/avg
	Item   *IterationOutput   `json:"item,omitempty"`   // For min/max: the winning item
	Values []any              `json:"values,omitempty"` // For distinct
	Groups map[string]any     `json:"groups,omitempty"` // For group_by
}

// KnowledgeStore provides storage and querying of task outputs
type KnowledgeStore interface {
	// StoreTaskOutput stores a task's output
	StoreTaskOutput(output TaskOutput)

	// GetTaskOutput returns a task's output by name
	GetTaskOutput(taskName string) (*TaskOutput, bool)

	// GetByItemIDs returns iterations matching the given item IDs
	GetByItemIDs(taskName string, itemIDs []string) []IterationOutput

	// Query returns iterations matching the query
	Query(taskName string, query Query) QueryResult

	// Aggregate performs an aggregate operation on iterations
	Aggregate(taskName string, query AggregateQuery) AggregateResult
}

// MemoryKnowledgeStore is an in-memory implementation of KnowledgeStore
type MemoryKnowledgeStore struct {
	mu      sync.RWMutex
	outputs map[string]*TaskOutput
}

// NewMemoryKnowledgeStore creates a new in-memory knowledge store
func NewMemoryKnowledgeStore() *MemoryKnowledgeStore {
	return &MemoryKnowledgeStore{
		outputs: make(map[string]*TaskOutput),
	}
}

// StoreTaskOutput stores a task's output
func (s *MemoryKnowledgeStore) StoreTaskOutput(output TaskOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs[output.TaskName] = &output
}

// GetTaskOutput returns a task's output by name
func (s *MemoryKnowledgeStore) GetTaskOutput(taskName string) (*TaskOutput, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	output, ok := s.outputs[taskName]
	return output, ok
}

// GetByItemIDs returns iterations matching the given item IDs
func (s *MemoryKnowledgeStore) GetByItemIDs(taskName string, itemIDs []string) []IterationOutput {
	s.mu.RLock()
	defer s.mu.RUnlock()

	output, ok := s.outputs[taskName]
	if !ok || !output.IsIterated {
		return nil
	}

	idSet := make(map[string]bool)
	for _, id := range itemIDs {
		idSet[id] = true
	}

	var results []IterationOutput
	for _, iter := range output.Iterations {
		if idSet[iter.ItemID] {
			results = append(results, iter)
		}
	}
	return results
}

// Query returns iterations matching the query
func (s *MemoryKnowledgeStore) Query(taskName string, query Query) QueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	output, ok := s.outputs[taskName]
	if !ok || !output.IsIterated {
		return QueryResult{}
	}

	// Filter iterations
	var matches []IterationOutput
	for _, iter := range output.Iterations {
		if matchesFilters(iter, query.Filters) {
			matches = append(matches, iter)
		}
	}

	totalMatches := len(matches)

	// Sort if order_by specified
	if query.OrderBy != "" {
		sortIterations(matches, query.OrderBy, query.Desc)
	}

	// Apply offset and limit
	if query.Offset > 0 {
		if query.Offset >= len(matches) {
			matches = nil
		} else {
			matches = matches[query.Offset:]
		}
	}
	if query.Limit > 0 && query.Limit < len(matches) {
		matches = matches[:query.Limit]
	}

	return QueryResult{
		TotalMatches: totalMatches,
		Results:      matches,
	}
}

// Aggregate performs an aggregate operation on iterations
func (s *MemoryKnowledgeStore) Aggregate(taskName string, query AggregateQuery) AggregateResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	output, ok := s.outputs[taskName]
	if !ok || !output.IsIterated {
		return AggregateResult{}
	}

	// Filter iterations first
	var matches []IterationOutput
	for _, iter := range output.Iterations {
		if matchesFilters(iter, query.Filters) {
			matches = append(matches, iter)
		}
	}

	switch query.Op {
	case AggCount:
		return AggregateResult{Value: len(matches)}

	case AggSum:
		sum := 0.0
		for _, iter := range matches {
			sum += getNumericValue(iter, query.Field)
		}
		return AggregateResult{Value: sum}

	case AggAvg:
		if len(matches) == 0 {
			return AggregateResult{Value: 0.0}
		}
		sum := 0.0
		for _, iter := range matches {
			sum += getNumericValue(iter, query.Field)
		}
		return AggregateResult{Value: sum / float64(len(matches))}

	case AggMin:
		if len(matches) == 0 {
			return AggregateResult{}
		}
		minIdx := 0
		minVal := getNumericValue(matches[0], query.Field)
		for i := 1; i < len(matches); i++ {
			v := getNumericValue(matches[i], query.Field)
			if v < minVal {
				minVal = v
				minIdx = i
			}
		}
		return AggregateResult{Value: minVal, Item: &matches[minIdx]}

	case AggMax:
		if len(matches) == 0 {
			return AggregateResult{}
		}
		maxIdx := 0
		maxVal := getNumericValue(matches[0], query.Field)
		for i := 1; i < len(matches); i++ {
			v := getNumericValue(matches[i], query.Field)
			if v > maxVal {
				maxVal = v
				maxIdx = i
			}
		}
		return AggregateResult{Value: maxVal, Item: &matches[maxIdx]}

	case AggDistinct:
		seen := make(map[any]bool)
		var values []any
		for _, iter := range matches {
			v := getFieldValue(iter, query.Field)
			if !seen[v] {
				seen[v] = true
				values = append(values, v)
			}
		}
		return AggregateResult{Values: values}

	case AggGroupBy:
		groups := make(map[string][]IterationOutput)
		for _, iter := range matches {
			key := fmt.Sprintf("%v", getFieldValue(iter, query.GroupBy))
			groups[key] = append(groups[key], iter)
		}

		result := make(map[string]any)
		for key, group := range groups {
			switch query.GroupOp {
			case AggCount:
				result[key] = len(group)
			case AggSum:
				sum := 0.0
				for _, iter := range group {
					sum += getNumericValue(iter, query.Field)
				}
				result[key] = sum
			case AggAvg:
				if len(group) == 0 {
					result[key] = 0.0
				} else {
					sum := 0.0
					for _, iter := range group {
						sum += getNumericValue(iter, query.Field)
					}
					result[key] = sum / float64(len(group))
				}
			case AggMin:
				if len(group) > 0 {
					minVal := getNumericValue(group[0], query.Field)
					for i := 1; i < len(group); i++ {
						v := getNumericValue(group[i], query.Field)
						if v < minVal {
							minVal = v
						}
					}
					result[key] = minVal
				}
			case AggMax:
				if len(group) > 0 {
					maxVal := getNumericValue(group[0], query.Field)
					for i := 1; i < len(group); i++ {
						v := getNumericValue(group[i], query.Field)
						if v > maxVal {
							maxVal = v
						}
					}
					result[key] = maxVal
				}
			default:
				result[key] = len(group) // Default to count
			}
		}
		return AggregateResult{Groups: result}

	default:
		return AggregateResult{}
	}
}

// matchesFilters checks if an iteration matches all filters
func matchesFilters(iter IterationOutput, filters []Filter) bool {
	for _, f := range filters {
		if !matchesFilter(iter, f) {
			return false
		}
	}
	return true
}

// matchesFilter checks if an iteration matches a single filter
func matchesFilter(iter IterationOutput, f Filter) bool {
	val := getFieldValue(iter, f.Field)
	if val == nil {
		return false
	}

	switch f.Op {
	case FilterEq:
		return compareValues(val, f.Value) == 0
	case FilterNe:
		return compareValues(val, f.Value) != 0
	case FilterGt:
		return compareValues(val, f.Value) > 0
	case FilterLt:
		return compareValues(val, f.Value) < 0
	case FilterGte:
		return compareValues(val, f.Value) >= 0
	case FilterLte:
		return compareValues(val, f.Value) <= 0
	case FilterContains:
		strVal, ok1 := val.(string)
		strFilter, ok2 := f.Value.(string)
		if ok1 && ok2 {
			return contains(strVal, strFilter)
		}
		return false
	default:
		return false
	}
}

// getFieldValue gets a field value from an iteration output
func getFieldValue(iter IterationOutput, field string) any {
	// Check standard fields first
	switch field {
	case "index":
		return iter.Index
	case "item_id":
		return iter.ItemID
	case "status":
		return iter.Status
	case "summary":
		return iter.Summary
	}

	// Check output map
	if iter.Output != nil {
		if val, ok := iter.Output[field]; ok {
			return val
		}
	}
	return nil
}

// getNumericValue gets a numeric field value from an iteration output
func getNumericValue(iter IterationOutput, field string) float64 {
	val := getFieldValue(iter, field)
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	default:
		return 0
	}
}

// compareValues compares two values, returning -1, 0, or 1
func compareValues(a, b any) int {
	// Try numeric comparison
	aNum, aOk := toFloat64(a)
	bNum, bOk := toFloat64(b)
	if aOk && bOk {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}

	// Fall back to string comparison
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)
	if aStr < bStr {
		return -1
	}
	if aStr > bStr {
		return 1
	}
	return 0
}

// toFloat64 attempts to convert a value to float64
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	default:
		return 0, false
	}
}

// contains checks if s contains substr (case-sensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr) >= 0)
}

// findSubstring returns the index of substr in s, or -1 if not found
func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// sortIterations sorts iterations by a field
func sortIterations(iters []IterationOutput, field string, desc bool) {
	sort.Slice(iters, func(i, j int) bool {
		vi := getFieldValue(iters[i], field)
		vj := getFieldValue(iters[j], field)
		cmp := compareValues(vi, vj)
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}
