// Package runstore stores content and call records within a root run's scope.
package runstore

import (
	"context"
	"errors"
	"fmt"
)

type (
	// Hash is a store-computed content address: "sha256:" followed by the
	// lowercase hexadecimal SHA-256 digest of the exact stored bytes.
	Hash string

	// CallKey identifies a model call within a scope.
	CallKey struct {
		RunID string
		// Seq comes from NextSeq and is unique per run across retries and
		// parallel calls.
		Seq int
		// Attempt is the attempt of the activity that made the call, as
		// reported by the workflow engine.
		Attempt int
	}

	// CallState describes whether a call started, completed, or failed.
	CallState string

	// CallRecord holds opaque, caller-encoded bodies. The caller owns their
	// encoding; stores preserve and compare bytes exactly, without decoding.
	CallRecord struct {
		Key   CallKey
		State CallState
		// Started encodes the request and start metadata and is required on
		// every write, including completion writes.
		Started []byte
		// Completion encodes the completion body. It is required for completed
		// and failed records, and must be empty for started records.
		Completion []byte
	}

	// Store holds content, calls and sequence counters. Every scope is a
	// required, opaque root run ID; data is never visible across scopes.
	Store interface {
		// Put computes the content hash and stores identical bytes only once
		// in each scope. Repeated writes return the same hash.
		Put(ctx context.Context, scope string, data []byte) (Hash, error)
		// Get returns the bytes, or ErrNotFound if this scope lacks the hash.
		Get(ctx context.Context, scope string, hash Hash) ([]byte, error)
		// NextSeq allocates 1, 2, 3, ... independently per (scope, runID),
		// without repetition even under concurrent callers.
		NextSeq(ctx context.Context, scope string, runID string) (int, error)
		// PutCall validates the record before writing. Identity is scope+Key.
		// A new record must be started. Started retries require identical
		// Started bytes; changes conflict. Started over a final record returns
		// ErrCallTransition. A completed/failed record may replace started
		// only with identical Started bytes, otherwise ErrCallConflict.
		// Final retries succeed only with the same state and both identical
		// bodies; all other final-over-final writes return ErrCallConflict.
		// Final writes without a started record return ErrCallTransition.
		PutCall(ctx context.Context, scope string, record CallRecord) error
		// ListCalls returns all of a run's records ordered by Seq then Attempt.
		// Started records remain started; readers classify them as incomplete.
		ListCalls(ctx context.Context, scope string, runID string) ([]CallRecord, error)
		// DeleteScope removes content, calls and counters. Repeating it is safe.
		DeleteScope(ctx context.Context, scope string) error
	}
)

const (
	// CallStarted records a request before the call is sent.
	CallStarted CallState = "started"
	// CallCompleted records a successful completion.
	CallCompleted CallState = "completed"
	// CallFailed records a failed completion.
	CallFailed CallState = "failed"
)

var (
	// ErrNotFound means the requested content does not exist in this scope.
	ErrNotFound = errors.New("runstore content not found")
	// ErrCallConflict means an existing call has a different body or final state.
	ErrCallConflict = errors.New("runstore call conflict")
	// ErrCallTransition means a call would skip or reverse its started state.
	ErrCallTransition = errors.New("runstore invalid call transition")
	// ErrContentTooLarge means content exceeds an implementation's size limit.
	ErrContentTooLarge = errors.New("runstore content too large")
)

// Validate checks the identity, state and body requirements before any write.
func (r CallRecord) Validate() error {
	switch {
	case r.Key.RunID == "":
		return fmt.Errorf("runstore call: run_id is required")
	case r.Key.Seq < 1:
		return fmt.Errorf("runstore call: seq must be positive")
	case r.Key.Attempt < 1:
		return fmt.Errorf("runstore call: attempt must be positive")
	case len(r.Started) == 0:
		return fmt.Errorf("runstore call: started body is required")
	}
	switch r.State {
	case CallStarted:
		if len(r.Completion) != 0 {
			return fmt.Errorf("runstore call: started record must not have a completion body")
		}
	case CallCompleted, CallFailed:
		if len(r.Completion) == 0 {
			return fmt.Errorf("runstore call: %s record requires a completion body", r.State)
		}
	default:
		return fmt.Errorf("runstore call: unknown state %q", r.State)
	}
	return nil
}
