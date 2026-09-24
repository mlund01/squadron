package store

import (
	"database/sql"
	"fmt"
	"time"
)

type SQLiteRuntimeStore struct{ db *sql.DB }

func (s *SQLiteRuntimeStore) CreateRuntime(checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	return createRuntime(s.db, DialectSQLite, checkpoint, events)
}

func (s *SQLiteRuntimeStore) CommitRuntime(runID string, expectedRevision uint64, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	return commitRuntime(s.db, DialectSQLite, runID, expectedRevision, checkpoint, events)
}

func (s *SQLiteRuntimeStore) LoadRuntime(runID string) (RuntimeCheckpointRecord, []RuntimeEventRecord, error) {
	return loadRuntime(s.db, DialectSQLite, runID)
}

type PgRuntimeStore struct{ db *sql.DB }

func (s *PgRuntimeStore) CreateRuntime(checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	return createRuntime(s.db, DialectPostgres, checkpoint, events)
}

func (s *PgRuntimeStore) CommitRuntime(runID string, expectedRevision uint64, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	return commitRuntime(s.db, DialectPostgres, runID, expectedRevision, checkpoint, events)
}

func (s *PgRuntimeStore) LoadRuntime(runID string) (RuntimeCheckpointRecord, []RuntimeEventRecord, error) {
	return loadRuntime(s.db, DialectPostgres, runID)
}

func createRuntime(db *sql.DB, dialect Dialect, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	if checkpoint.RunID == "" {
		return fmt.Errorf("runtime checkpoint run ID is required")
	}
	if err := validateRuntimeEvents(checkpoint.RunID, events); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin runtime creation: %w", err)
	}
	defer tx.Rollback()

	checkpointSQL := `INSERT INTO runtime_checkpoints (run_id, revision, payload_json, updated_at) VALUES (?, ?, ?, ?)`
	if dialect == DialectPostgres {
		checkpointSQL = `INSERT INTO runtime_checkpoints (run_id, revision, payload_json, updated_at) VALUES ($1, $2, $3, $4)`
	}
	if _, err := tx.Exec(checkpointSQL, checkpoint.RunID, checkpoint.Revision, checkpoint.PayloadJSON, formatRuntimeTime(checkpoint.UpdatedAt)); err != nil {
		return fmt.Errorf("insert runtime checkpoint: %w", err)
	}
	if err := insertRuntimeEvents(tx, dialect, checkpoint.RunID, 1, events); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime creation: %w", err)
	}
	return nil
}

func commitRuntime(db *sql.DB, dialect Dialect, runID string, expectedRevision uint64, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	if checkpoint.RunID != runID {
		return fmt.Errorf("checkpoint belongs to run %q, want %q", checkpoint.RunID, runID)
	}
	if err := validateRuntimeEvents(runID, events); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin runtime commit: %w", err)
	}
	defer tx.Rollback()

	updateSQL := `UPDATE runtime_checkpoints SET revision = ?, payload_json = ?, updated_at = ? WHERE run_id = ? AND revision = ?`
	if dialect == DialectPostgres {
		updateSQL = `UPDATE runtime_checkpoints SET revision = $1, payload_json = $2, updated_at = $3 WHERE run_id = $4 AND revision = $5`
	}
	result, err := tx.Exec(updateSQL, checkpoint.Revision, checkpoint.PayloadJSON, formatRuntimeTime(checkpoint.UpdatedAt), runID, expectedRevision)
	if err != nil {
		return fmt.Errorf("update runtime checkpoint: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect runtime checkpoint update: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("run %q revision conflict: expected %d", runID, expectedRevision)
	}

	maxSequenceSQL := `SELECT COALESCE(MAX(sequence), 0) FROM runtime_events WHERE run_id = ?`
	if dialect == DialectPostgres {
		maxSequenceSQL = `SELECT COALESCE(MAX(sequence), 0) FROM runtime_events WHERE run_id = $1`
	}
	var maxSequence uint64
	if err := tx.QueryRow(maxSequenceSQL, runID).Scan(&maxSequence); err != nil {
		return fmt.Errorf("read runtime event sequence: %w", err)
	}
	if err := insertRuntimeEvents(tx, dialect, runID, maxSequence+1, events); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime transition: %w", err)
	}
	return nil
}

func loadRuntime(db *sql.DB, dialect Dialect, runID string) (RuntimeCheckpointRecord, []RuntimeEventRecord, error) {
	checkpointSQL := `SELECT run_id, revision, payload_json, updated_at FROM runtime_checkpoints WHERE run_id = ?`
	eventsSQL := `SELECT sequence, run_id, revision, event_type, occurred_at, correlation_id, payload_json FROM runtime_events WHERE run_id = ? ORDER BY sequence`
	if dialect == DialectPostgres {
		checkpointSQL = `SELECT run_id, revision, payload_json, updated_at FROM runtime_checkpoints WHERE run_id = $1`
		eventsSQL = `SELECT sequence, run_id, revision, event_type, occurred_at, correlation_id, payload_json FROM runtime_events WHERE run_id = $1 ORDER BY sequence`
	}
	var checkpoint RuntimeCheckpointRecord
	var updatedAt string
	if err := db.QueryRow(checkpointSQL, runID).Scan(&checkpoint.RunID, &checkpoint.Revision, &checkpoint.PayloadJSON, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return RuntimeCheckpointRecord{}, nil, fmt.Errorf("run %q not found", runID)
		}
		return RuntimeCheckpointRecord{}, nil, fmt.Errorf("load runtime checkpoint: %w", err)
	}
	var err error
	checkpoint.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return RuntimeCheckpointRecord{}, nil, fmt.Errorf("parse runtime checkpoint timestamp: %w", err)
	}
	rows, err := db.Query(eventsSQL, runID)
	if err != nil {
		return RuntimeCheckpointRecord{}, nil, fmt.Errorf("load runtime events: %w", err)
	}
	defer rows.Close()
	var events []RuntimeEventRecord
	for rows.Next() {
		var event RuntimeEventRecord
		var occurredAt string
		if err := rows.Scan(&event.Sequence, &event.RunID, &event.Revision, &event.EventType, &occurredAt, &event.CorrelationID, &event.PayloadJSON); err != nil {
			return RuntimeCheckpointRecord{}, nil, fmt.Errorf("scan runtime event: %w", err)
		}
		event.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return RuntimeCheckpointRecord{}, nil, fmt.Errorf("parse runtime event timestamp: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return RuntimeCheckpointRecord{}, nil, fmt.Errorf("iterate runtime events: %w", err)
	}
	return checkpoint, events, nil
}

func insertRuntimeEvents(tx *sql.Tx, dialect Dialect, runID string, firstSequence uint64, events []RuntimeEventRecord) error {
	insertSQL := `INSERT INTO runtime_events (run_id, sequence, revision, event_type, occurred_at, correlation_id, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if dialect == DialectPostgres {
		insertSQL = `INSERT INTO runtime_events (run_id, sequence, revision, event_type, occurred_at, correlation_id, payload_json) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	}
	for index, event := range events {
		sequence := firstSequence + uint64(index)
		if _, err := tx.Exec(insertSQL, runID, sequence, event.Revision, event.EventType, formatRuntimeTime(event.OccurredAt), event.CorrelationID, event.PayloadJSON); err != nil {
			return fmt.Errorf("insert runtime event %d: %w", sequence, err)
		}
	}
	return nil
}

func validateRuntimeEvents(runID string, events []RuntimeEventRecord) error {
	for index, event := range events {
		if event.RunID != runID {
			return fmt.Errorf("runtime event %d belongs to run %q, want %q", index, event.RunID, runID)
		}
		if event.EventType == "" {
			return fmt.Errorf("runtime event %d type is required", index)
		}
	}
	return nil
}

func formatRuntimeTime(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}
