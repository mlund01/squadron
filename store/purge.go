package store

import (
	"database/sql"
	"fmt"
	"time"
)

// purgeExpiredMissions deletes finished missions older than the cutoff and
// every related row. cutoffPlaceholder is "?" (SQLite) or "$1" (Postgres).
//
// Child tables are deleted explicitly: most FKs have no ON DELETE CASCADE,
// and SQLite is opened without foreign_keys=ON, so we cannot rely on the
// engine to walk the tree.
func purgeExpiredMissions(db *sql.DB, olderThan time.Time, cutoffPlaceholder string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin purge: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS _squadron_purge_expired (id TEXT PRIMARY KEY)`); err != nil {
		return 0, fmt.Errorf("create purge temp table: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM _squadron_purge_expired`); err != nil {
		return 0, fmt.Errorf("clear purge temp table: %w", err)
	}

	insert := `INSERT INTO _squadron_purge_expired
		SELECT id FROM missions
		WHERE status NOT IN ('running', 'stopping')
		  AND COALESCE(finished_at, started_at) < ` + cutoffPlaceholder
	if _, err := tx.Exec(insert, tsFrom(olderThan)); err != nil {
		return 0, fmt.Errorf("select expired missions: %w", err)
	}

	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM _squadron_purge_expired`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count expired missions: %w", err)
	}
	if n == 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit purge: %w", err)
		}
		return 0, nil
	}

	stmts := []string{
		`DELETE FROM session_message_parts WHERE message_id IN (
			SELECT sm.id FROM session_messages sm
			INNER JOIN sessions s ON s.id = sm.session_id
			INNER JOIN mission_tasks t ON t.id = s.task_id
			WHERE t.mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM session_messages WHERE session_id IN (
			SELECT s.id FROM sessions s
			INNER JOIN mission_tasks t ON t.id = s.task_id
			WHERE t.mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM tool_results WHERE task_id IN (
			SELECT id FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM turn_costs WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM mission_events WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM mission_task_subtasks WHERE task_id IN (
			SELECT id FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM task_inputs WHERE task_id IN (
			SELECT id FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM task_outputs WHERE task_id IN (
			SELECT id FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM dataset_items WHERE dataset_id IN (
			SELECT id FROM datasets WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM datasets WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM route_decisions WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM human_input_requests WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM sessions WHERE task_id IN (
			SELECT id FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired))`,
		`DELETE FROM mission_tasks WHERE mission_id IN (SELECT id FROM _squadron_purge_expired)`,
		`DELETE FROM missions WHERE id IN (SELECT id FROM _squadron_purge_expired)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return 0, fmt.Errorf("purge delete: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit purge: %w", err)
	}
	return n, nil
}
