package localevidence

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// PutCall enforces the existing canonical lifecycle in an immediate transaction.
func (s *Store) PutCall(ctx context.Context, scope string, record runstore.CallRecord) (err error) {
	if scope == "" {
		return errors.New("local evidence: scope is required")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &err)
	var existing runstore.CallRecord
	err = tx.QueryRowContext(ctx, "SELECT state,started,completion FROM calls WHERE scope=? AND run_id=? AND seq=? AND attempt=?", scope, record.Key.RunID, record.Key.Seq, record.Key.Attempt).Scan(&existing.State, &existing.Started, &existing.Completion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := callTransition(existing, err == nil, record); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO calls(scope,run_id,seq,attempt,state,started,completion) VALUES(?,?,?,?,?,?,?) ON CONFLICT(scope,run_id,seq,attempt) DO UPDATE SET state=excluded.state,completion=excluded.completion", scope, record.Key.RunID, record.Key.Seq, record.Key.Attempt, string(record.State), record.Started, record.Completion)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ListCalls preserves every attempt in sequence/attempt order, including starts.
func (s *Store) ListCalls(ctx context.Context, scope, runID string) (records []runstore.CallRecord, err error) {
	if scope == "" {
		return nil, errors.New("local evidence: scope is required")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT seq,attempt,state,started,completion FROM calls WHERE scope=? AND run_id=? ORDER BY seq,attempt", scope, runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, rows.Close())
	}()
	for rows.Next() {
		record := runstore.CallRecord{Key: runstore.CallKey{RunID: runID}}
		if err := rows.Scan(&record.Key.Seq, &record.Key.Attempt, &record.State, &record.Started, &record.Completion); err != nil {
			return nil, err
		}
		if err := record.Validate(); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func callTransition(existing runstore.CallRecord, exists bool, next runstore.CallRecord) error {
	if !exists {
		if next.State != runstore.CallStarted {
			return runstore.ErrCallTransition
		}
		return nil
	}
	if next.State == runstore.CallStarted && existing.State != runstore.CallStarted {
		return runstore.ErrCallTransition
	}
	if !bytes.Equal(existing.Started, next.Started) {
		return runstore.ErrCallConflict
	}
	if existing.State != runstore.CallStarted && (existing.State != next.State || !bytes.Equal(existing.Completion, next.Completion)) {
		return runstore.ErrCallConflict
	}
	return nil
}
