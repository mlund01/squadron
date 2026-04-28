package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SQLiteHumanInputStore backs HumanInputStore with SQLite.
type SQLiteHumanInputStore struct {
	db *sql.DB
}

func (s *SQLiteHumanInputStore) CreateRequest(req *HumanInputRequestRecord) error {
	if req.ToolCallID == "" {
		return fmt.Errorf("tool_call_id required")
	}
	if req.ID == "" {
		req.ID = generateID()
	}
	if req.State == "" {
		req.State = HumanInputStateOpen
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}

	choicesJSON, err := marshalChoices(req.Choices)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO human_input_requests
		    (id, mission_id, task_id, tool_call_id, question, short_summary, additional_context, choices_json, multi_select,
		     state, requested_at, resolved_at, response, responder_user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tool_call_id) DO NOTHING`,
		req.ID, nullIfEmpty(req.MissionID), nullIfEmpty(req.TaskID),
		req.ToolCallID, req.Question, nullIfEmpty(req.ShortSummary), nullIfEmpty(req.AdditionalContext), choicesJSON, boolToInt(req.MultiSelect), req.State,
		tsFrom(req.RequestedAt), tsFromPtr(req.ResolvedAt),
		req.Response, req.ResponderUserID,
	)
	if err != nil {
		return fmt.Errorf("insert human input request: %w", err)
	}
	return nil
}

func (s *SQLiteHumanInputStore) GetByToolCallID(toolCallID string) (*HumanInputRequestRecord, error) {
	row := s.db.QueryRow(
		`SELECT id, mission_id, task_id, tool_call_id, question, short_summary, additional_context, choices_json, multi_select,
		        state, requested_at, resolved_at, response, responder_user_id
		 FROM human_input_requests WHERE tool_call_id = ?`,
		toolCallID,
	)
	return scanHumanInputRequest(row)
}

func (s *SQLiteHumanInputStore) ResolveRequest(toolCallID, response, responderUserID string) (*HumanInputRequestRecord, error) {
	now := time.Now().UTC()
	result, err := s.db.Exec(
		`UPDATE human_input_requests
		    SET state = ?, resolved_at = ?, response = ?, responder_user_id = ?
		  WHERE tool_call_id = ? AND state = ?`,
		HumanInputStateResolved, tsFrom(now), response, responderUserID,
		toolCallID, HumanInputStateOpen,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve human input request: %w", err)
	}
	// On miss (already resolved or not found) read back so the caller
	// sees the canonical state; GetByToolCallID returns sql.ErrNoRows
	// for a truly missing row.
	_ = result
	return s.GetByToolCallID(toolCallID)
}

func (s *SQLiteHumanInputStore) ListRequests(filter HumanInputFilter) ([]HumanInputRequestRecord, int, error) {
	where := ""
	args := []any{}
	if filter.State != "" {
		where += " AND state = ?"
		args = append(args, filter.State)
	}
	if filter.MissionID != "" {
		where += " AND mission_id = ?"
		args = append(args, filter.MissionID)
	}

	var total int
	countArgs := append([]any{}, args...)
	if err := s.db.QueryRow("SELECT COUNT(*) FROM human_input_requests WHERE 1=1"+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count human input requests: %w", err)
	}

	order := " ORDER BY requested_at DESC"
	if filter.OldestFirst {
		order = " ORDER BY requested_at ASC"
	}
	q := `SELECT id, mission_id, task_id, tool_call_id, question, short_summary, additional_context, choices_json, multi_select,
	              state, requested_at, resolved_at, response, responder_user_id
	       FROM human_input_requests WHERE 1=1` + where + order
	listArgs := append([]any{}, args...)
	if filter.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		listArgs = append(listArgs, filter.Limit, filter.Offset)
	}

	rows, err := s.db.Query(q, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list human input requests: %w", err)
	}
	defer rows.Close()
	out := []HumanInputRequestRecord{}
	for rows.Next() {
		rec, err := scanHumanInputRequest(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *rec)
	}
	return out, total, rows.Err()
}

// humanInputRowScanner is the common interface over *sql.Row and *sql.Rows.
type humanInputRowScanner interface {
	Scan(dest ...any) error
}

func scanHumanInputRequest(r humanInputRowScanner) (*HumanInputRequestRecord, error) {
	var (
		rec                                                              HumanInputRequestRecord
		missionID, taskID, shortSummary, additionalContext, choicesJSON sql.NullString
		resolvedAtStr, response, responderUserID                         sql.NullString
		requestedAtStr                                                   string
		multiSelectInt                                                   int
	)
	err := r.Scan(
		&rec.ID, &missionID, &taskID, &rec.ToolCallID, &rec.Question, &shortSummary, &additionalContext, &choicesJSON, &multiSelectInt,
		&rec.State, &requestedAtStr, &resolvedAtStr, &response, &responderUserID,
	)
	rec.MultiSelect = multiSelectInt != 0
	if err != nil {
		return nil, err
	}
	if missionID.Valid {
		rec.MissionID = missionID.String
	}
	if taskID.Valid {
		rec.TaskID = taskID.String
	}
	if shortSummary.Valid {
		rec.ShortSummary = shortSummary.String
	}
	if additionalContext.Valid {
		rec.AdditionalContext = additionalContext.String
	}
	if choicesJSON.Valid && choicesJSON.String != "" {
		if err := json.Unmarshal([]byte(choicesJSON.String), &rec.Choices); err != nil {
			return nil, fmt.Errorf("decode choices_json: %w", err)
		}
	}
	t, err := tsParse(requestedAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse requested_at: %w", err)
	}
	rec.RequestedAt = t
	rec.ResolvedAt, err = tsParseNull(resolvedAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse resolved_at: %w", err)
	}
	if response.Valid {
		s := response.String
		rec.Response = &s
	}
	if responderUserID.Valid {
		s := responderUserID.String
		rec.ResponderUserID = &s
	}
	return &rec, nil
}

func marshalChoices(choices []string) (any, error) {
	if len(choices) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(choices)
	if err != nil {
		return nil, fmt.Errorf("encode choices: %w", err)
	}
	return string(b), nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// boolToInt is sqlite-friendly: the schema column is INTEGER (0|1)
// because sqlite has no native boolean type — driver-level mappings
// vary, so we serialize explicitly to match the migration column type.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
