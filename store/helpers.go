package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// tsFormat formats a time.Time as an ISO-8601 string with millisecond precision.
const tsFormat = "2006-01-02T15:04:05.000Z"

func tsNow() string { return time.Now().UTC().Format(tsFormat) }
func tsFrom(t time.Time) string { return t.UTC().Format(tsFormat) }
func tsFromPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := tsFrom(*t)
	return &s
}

// tsParse parses an ISO-8601 timestamp string back to time.Time.
// Falls back to second-precision format if millisecond parse fails.
func tsParse(s string) (time.Time, error) {
	t, err := time.Parse(tsFormat, s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z", s)
	}
	return t, err
}

// tsParseNull parses an optional timestamp string to *time.Time.
func tsParseNull(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := tsParse(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// =============================================================================
// cty conversion helpers
// =============================================================================

// ctyValueToGo converts a cty.Value to a Go value for JSON serialization
func ctyValueToGo(val cty.Value) any {
	if val.IsNull() || !val.IsKnown() {
		return nil
	}

	switch {
	case val.Type() == cty.String:
		return val.AsString()
	case val.Type() == cty.Number:
		f, _ := val.AsBigFloat().Float64()
		return f
	case val.Type() == cty.Bool:
		return val.True()
	case val.Type().IsObjectType() || val.Type().IsMapType():
		result := make(map[string]any)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			result[k.AsString()] = ctyValueToGo(v)
		}
		return result
	case val.Type().IsTupleType() || val.Type().IsListType():
		var result []any
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			result = append(result, ctyValueToGo(v))
		}
		return result
	default:
		return nil
	}
}

// goToCtyValue converts a Go value to cty.Value
func goToCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case string:
		return cty.StringVal(val)
	case float64:
		return cty.NumberFloatVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case bool:
		return cty.BoolVal(val)
	case map[string]any:
		if len(val) == 0 {
			return cty.EmptyObjectVal
		}
		vals := make(map[string]cty.Value)
		for k, v := range val {
			vals[k] = goToCtyValue(v)
		}
		return cty.ObjectVal(vals)
	case []any:
		if len(val) == 0 {
			return cty.ListValEmpty(cty.DynamicPseudoType)
		}
		vals := make([]cty.Value, len(val))
		for i, item := range val {
			vals[i] = goToCtyValue(item)
		}
		return cty.TupleVal(vals)
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	default:
		return cty.StringVal(fmt.Sprintf("%v", v))
	}
}

// goJSONToCty parses a JSON string into a cty.Value
func goJSONToCty(jsonStr string) cty.Value {
	var v any
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return cty.StringVal(jsonStr)
	}
	return goToCtyValue(v)
}

// scanSubtasks scans subtask rows from a database query.
// Expects columns: id, task_id, session_id, iteration_index, idx, title, status, created_at, completed_at
func scanSubtasks(rows *sql.Rows) ([]Subtask, error) {
	var subtasks []Subtask
	for rows.Next() {
		var st Subtask
		var iterIdx sql.NullInt64
		var createdAtStr string
		var completedAtStr sql.NullString
		if err := rows.Scan(&st.ID, &st.TaskID, &st.SessionID, &iterIdx, &st.Index, &st.Title, &st.Status, &createdAtStr, &completedAtStr); err != nil {
			return nil, err
		}
		st.CreatedAt, _ = tsParse(createdAtStr)
		st.CompletedAt, _ = tsParseNull(completedAtStr)
		if iterIdx.Valid {
			idx := int(iterIdx.Int64)
			st.IterationIndex = &idx
		}
		subtasks = append(subtasks, st)
	}
	return subtasks, nil
}

// scanEvents scans mission event rows from a database query.
func scanEvents(rows *sql.Rows) ([]MissionEvent, error) {
	var events []MissionEvent
	for rows.Next() {
		var e MissionEvent
		var taskID, sessionID sql.NullString
		var iterIdx sql.NullInt64
		var createdAtStr string

		if err := rows.Scan(&e.ID, &e.MissionID, &taskID, &sessionID, &iterIdx, &e.EventType, &e.DataJSON, &createdAtStr); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = tsParse(createdAtStr)
		if taskID.Valid {
			e.TaskID = &taskID.String
		}
		if sessionID.Valid {
			e.SessionID = &sessionID.String
		}
		if iterIdx.Valid {
			idx := int(iterIdx.Int64)
			e.IterationIndex = &idx
		}
		events = append(events, e)
	}
	return events, nil
}
