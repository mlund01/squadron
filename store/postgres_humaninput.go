package store

import (
	"database/sql"
	"fmt"
	"time"
)

// PgHumanInputStore is the Postgres mirror of SQLiteHumanInputStore.
// SQL syntax differences with SQLite are limited to placeholder style
// ($N vs ?) and ON CONFLICT semantics (both support the DO NOTHING
// clause the same way).
type PgHumanInputStore struct {
	db *sql.DB
}

func (s *PgHumanInputStore) CreateRequest(req *HumanInputRequestRecord) error {
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
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT(tool_call_id) DO NOTHING`,
		req.ID, nullIfEmpty(req.MissionID), nullIfEmpty(req.TaskID),
		req.ToolCallID, req.Question, nullIfEmpty(req.ShortSummary), nullIfEmpty(req.AdditionalContext), choicesJSON, req.MultiSelect, req.State,
		req.RequestedAt.UTC(), req.ResolvedAt,
		req.Response, req.ResponderUserID,
	)
	if err != nil {
		return fmt.Errorf("insert human input request: %w", err)
	}
	return nil
}

func (s *PgHumanInputStore) GetByToolCallID(toolCallID string) (*HumanInputRequestRecord, error) {
	row := s.db.QueryRow(
		`SELECT id, mission_id, task_id, tool_call_id, question, short_summary, additional_context, choices_json, multi_select,
		        state, requested_at, resolved_at, response, responder_user_id
		 FROM human_input_requests WHERE tool_call_id = $1`,
		toolCallID,
	)
	return scanHumanInputRequestPG(row)
}

func (s *PgHumanInputStore) ResolveRequest(toolCallID, response, responderUserID string) (*HumanInputRequestRecord, error) {
	_, err := s.db.Exec(
		`UPDATE human_input_requests
		    SET state = $1, resolved_at = $2, response = $3, responder_user_id = $4
		  WHERE tool_call_id = $5 AND state = $6`,
		HumanInputStateResolved, time.Now().UTC(), response, responderUserID,
		toolCallID, HumanInputStateOpen,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve human input request: %w", err)
	}
	return s.GetByToolCallID(toolCallID)
}

func (s *PgHumanInputStore) ListRequests(filter HumanInputFilter) ([]HumanInputRequestRecord, int, error) {
	where := ""
	args := []any{}
	idx := 1
	nextArg := func(v any) string {
		args = append(args, v)
		p := fmt.Sprintf("$%d", idx)
		idx++
		return p
	}
	if filter.State != "" {
		where += " AND state = " + nextArg(filter.State)
	}
	if filter.MissionID != "" {
		where += " AND mission_id = " + nextArg(filter.MissionID)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM human_input_requests WHERE 1=1"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count human input requests: %w", err)
	}

	order := " ORDER BY requested_at DESC"
	if filter.OldestFirst {
		order = " ORDER BY requested_at ASC"
	}
	q := `SELECT id, mission_id, task_id, tool_call_id, question, short_summary, additional_context, choices_json, multi_select,
	              state, requested_at, resolved_at, response, responder_user_id
	       FROM human_input_requests WHERE 1=1` + where + order
	if filter.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, filter.Offset)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list human input requests: %w", err)
	}
	defer rows.Close()
	out := []HumanInputRequestRecord{}
	for rows.Next() {
		rec, err := scanHumanInputRequestPG(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *rec)
	}
	return out, total, rows.Err()
}

// scanHumanInputRequestPG scans a row/rows where the timestamp columns
// come back as time.Time (Postgres native) rather than strings (SQLite).
func scanHumanInputRequestPG(r humanInputRowScanner) (*HumanInputRequestRecord, error) {
	var (
		rec                                                              HumanInputRequestRecord
		missionID, taskID, shortSummary, additionalContext, choicesJSON sql.NullString
		response, responderUserID                                        sql.NullString
		requestedAt                                                      time.Time
		resolvedAt                                                       sql.NullTime
	)
	err := r.Scan(
		&rec.ID, &missionID, &taskID, &rec.ToolCallID, &rec.Question, &shortSummary, &additionalContext, &choicesJSON, &rec.MultiSelect,
		&rec.State, &requestedAt, &resolvedAt, &response, &responderUserID,
	)
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
		if err := unmarshalChoices(choicesJSON.String, &rec.Choices); err != nil {
			return nil, err
		}
	}
	rec.RequestedAt = requestedAt
	if resolvedAt.Valid {
		t := resolvedAt.Time
		rec.ResolvedAt = &t
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
