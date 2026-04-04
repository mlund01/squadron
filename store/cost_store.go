package store

import (
	"database/sql"
	"time"
)

// SQLiteCostStore implements CostStore backed by SQLite.
type SQLiteCostStore struct {
	db *sql.DB
}

func (s *SQLiteCostStore) StoreTurnCost(cost TurnCostRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO turn_costs (id, mission_id, task_id, session_id, mission_name, task_name, entity, model,
		 input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
		 input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost,
		 duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(), cost.MissionID, cost.TaskID, cost.SessionID,
		cost.MissionName, cost.TaskName, cost.Entity, cost.Model,
		cost.InputTokens, cost.OutputTokens, cost.CacheWriteTokens, cost.CacheReadTokens,
		cost.InputCost, cost.OutputCost, cost.CacheReadCost, cost.CacheWriteCost, cost.TotalCost,
		cost.DurationMs, tsNow(),
	)
	return err
}

func (s *SQLiteCostStore) GetCostsByMission(missionID string) ([]TurnCostRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, mission_id, task_id, session_id, mission_name, task_name, entity, model,
		 input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
		 input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost,
		 duration_ms, created_at FROM turn_costs WHERE mission_id = ? ORDER BY created_at`,
		missionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTurnCosts(rows)
}

func (s *SQLiteCostStore) GetCostSummary(from, to time.Time, groupBy string) ([]CostSummaryRow, error) {
	// groupBy: "model", "mission_name", "date"
	groupExpr := groupBy
	if groupBy == "date" {
		groupExpr = "substr(created_at, 1, 10)" // YYYY-MM-DD
	}

	rows, err := s.db.Query(
		`SELECT `+groupExpr+` AS group_key, COUNT(*) AS turns,
		 SUM(total_cost) AS total_cost, SUM(input_cost) AS input_cost, SUM(output_cost) AS output_cost,
		 SUM(cache_read_cost) AS cache_read_cost, SUM(cache_write_cost) AS cache_write_cost
		 FROM turn_costs WHERE created_at >= ? AND created_at <= ?
		 GROUP BY group_key ORDER BY total_cost DESC`,
		tsFrom(from), tsFrom(to),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CostSummaryRow
	for rows.Next() {
		var r CostSummaryRow
		if err := rows.Scan(&r.GroupKey, &r.Turns, &r.TotalCost, &r.InputCost, &r.OutputCost, &r.CacheReadCost, &r.CacheWriteCost); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *SQLiteCostStore) GetRecentMissionCosts(limit int) ([]MissionCostRow, error) {
	rows, err := s.db.Query(
		`SELECT tc.mission_id, tc.mission_name, COALESCE(m.status, 'unknown'),
		 COUNT(*) AS turns, SUM(tc.total_cost) AS total_cost, MIN(tc.created_at)
		 FROM turn_costs tc
		 LEFT JOIN missions m ON m.id = tc.mission_id
		 GROUP BY tc.mission_id
		 ORDER BY MIN(tc.created_at) DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MissionCostRow
	for rows.Next() {
		var r MissionCostRow
		var startedAtStr string
		if err := rows.Scan(&r.MissionID, &r.MissionName, &r.Status, &r.Turns, &r.TotalCost, &startedAtStr); err != nil {
			return nil, err
		}
		r.StartedAt, _ = tsParse(startedAtStr)
		results = append(results, r)
	}
	return results, nil
}

func (s *SQLiteCostStore) GetCostsByDateAndField(from, to time.Time, field string) ([]DateFieldCostRow, error) {
	// field: "model" or "mission_name"
	rows, err := s.db.Query(
		`SELECT substr(created_at, 1, 10) AS date, `+field+` AS field_key, SUM(total_cost) AS total_cost
		 FROM turn_costs WHERE created_at >= ? AND created_at <= ?
		 GROUP BY date, field_key ORDER BY date, total_cost DESC`,
		tsFrom(from), tsFrom(to),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DateFieldCostRow
	for rows.Next() {
		var r DateFieldCostRow
		if err := rows.Scan(&r.Date, &r.FieldKey, &r.TotalCost); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *SQLiteCostStore) GetTotalCosts(from, to time.Time) (*CostTotals, error) {
	row := s.db.QueryRow(
		`SELECT COALESCE(SUM(total_cost), 0), COALESCE(SUM(input_cost), 0), COALESCE(SUM(output_cost), 0),
		 COALESCE(SUM(cache_read_cost), 0), COALESCE(SUM(cache_write_cost), 0),
		 COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		 FROM turn_costs WHERE created_at >= ? AND created_at <= ?`,
		tsFrom(from), tsFrom(to),
	)

	var t CostTotals
	if err := row.Scan(&t.TotalCost, &t.InputCost, &t.OutputCost,
		&t.CacheReadCost, &t.CacheWriteCost,
		&t.TotalTurns, &t.TotalInputTokens, &t.TotalOutputTokens); err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTurnCosts(rows *sql.Rows) ([]TurnCostRecord, error) {
	var results []TurnCostRecord
	for rows.Next() {
		var r TurnCostRecord
		var createdAtStr string
		if err := rows.Scan(&r.ID, &r.MissionID, &r.TaskID, &r.SessionID,
			&r.MissionName, &r.TaskName, &r.Entity, &r.Model,
			&r.InputTokens, &r.OutputTokens, &r.CacheWriteTokens, &r.CacheReadTokens,
			&r.InputCost, &r.OutputCost, &r.CacheReadCost, &r.CacheWriteCost, &r.TotalCost,
			&r.DurationMs, &createdAtStr); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = tsParse(createdAtStr)
		results = append(results, r)
	}
	return results, nil
}
