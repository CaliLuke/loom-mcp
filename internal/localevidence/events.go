package localevidence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

type storedEvent struct {
	Event   runlog.Event
	Payload []byte
}

// Append commits immutable event bytes, deduplicated by run and logical key.
func (s *Store) Append(ctx context.Context, event *runlog.Event) (result runlog.AppendResult, err error) {
	body, err := encodeEvent(event)
	if err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer rollback(tx, &err)
	id, inserted, err := appendEvent(ctx, tx, event, body)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.ID = strconv.FormatInt(id, 10)
	result.Inserted = inserted
	event.ID = result.ID
	return result, nil
}

// List returns a forward page and preserves the original JSON payload bytes.
func (s *Store) List(ctx context.Context, runID, cursor string, limit int) (page runlog.Page, err error) {
	if runID == "" || limit <= 0 {
		return page, errors.New("local evidence: run_id and positive limit are required")
	}
	var after int64
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after <= 0 {
			return page, fmt.Errorf("local evidence: invalid cursor %q", cursor)
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,body FROM events WHERE run_id=? AND id>? ORDER BY id LIMIT ?", runID, after, int64(limit)+1)
	if err != nil {
		return page, err
	}
	defer func() {
		err = errors.Join(err, rows.Close())
	}()
	for rows.Next() {
		if len(page.Events) == limit {
			page.NextCursor = page.Events[len(page.Events)-1].ID
			break
		}
		var id int64
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return page, err
		}
		var stored storedEvent
		if err := json.Unmarshal(body, &stored, json.RejectUnknownMembers(true)); err != nil {
			return page, fmt.Errorf("local evidence: corrupt event %d: %w", id, err)
		}
		stored.Event.ID = strconv.FormatInt(id, 10)
		stored.Event.Payload = rawjson.Message(stored.Payload)
		page.Events = append(page.Events, &stored.Event)
	}
	return page, rows.Err()
}

func encodeEvent(event *runlog.Event) ([]byte, error) {
	if event == nil || event.RunID == "" || event.EventKey == "" {
		return nil, errors.New("local evidence: event, run_id and event_key are required")
	}
	copy := *event
	copy.ID, copy.Payload, copy.Timestamp = "", nil, copy.Timestamp.UTC()
	payload := []byte(event.Payload)
	if len(payload) == 0 {
		payload = nil
	}
	return json.Marshal(storedEvent{Event: copy, Payload: payload}, json.Deterministic(true))
}

func appendEvent(ctx context.Context, tx *sql.Tx, event *runlog.Event, body []byte) (int64, bool, error) {
	var id int64
	var existing []byte
	err := tx.QueryRowContext(ctx, "SELECT id,body FROM events WHERE run_id=? AND event_key=?", event.RunID, event.EventKey).Scan(&id, &existing)
	if errors.Is(err, sql.ErrNoRows) {
		inserted, err := tx.ExecContext(ctx, "INSERT INTO events(run_id,event_key,body) VALUES(?,?,?)", event.RunID, event.EventKey, body)
		if err != nil {
			return 0, false, err
		}
		id, err = inserted.LastInsertId()
		return id, true, err
	}
	if err != nil {
		return 0, false, err
	}
	if !bytes.Equal(existing, body) {
		return 0, false, fmt.Errorf("local evidence: event key %q conflict", event.EventKey)
	}
	return id, false, nil
}
