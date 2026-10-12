package session

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Continuation is the immutable, rendered carry of one completed root run.
	// ID is the run ID; Prev and Pos identify its place in the session chain.
	Continuation struct {
		ID         string          `json:"id"`
		Prev       *string         `json:"prev"`
		Pos        int             `json:"pos"`
		Checkpoint *runstore.Hash  `json:"checkpoint"`
		Pins       []Carried       `json:"pins"`
		UserIndex  []Carried       `json:"user_index"`
		Tail       []Carried       `json:"tail"`
		Visible    []runstore.Hash `json:"visible"`
	}
	// Carried retains provenance and only the bytes actually rendered at sealing.
	Carried struct {
		Entry index.MessageEntry `json:"entry"`
		Bytes *runstore.Hash     `json:"bytes"`
	}
	// AdvanceOutcome describes an atomic compare-and-swap of the session head.
	AdvanceOutcome string
	// ContinuationStore persists immutable continuation bodies and a session head.
	// Implementations compare deterministic JSON bodies by ID before checking the
	// expected head. Inserting the body and moving the head must be atomic.
	ContinuationStore interface {
		AdvanceContinuation(context.Context, string, *string, Continuation) (AdvanceOutcome, error)
		LoadContinuationHead(context.Context, string) (*Continuation, error)
		LoadContinuation(context.Context, string, string) (Continuation, error)
	}
)

const (
	// Published means this invocation inserted the body and advanced the head.
	Published AdvanceOutcome = "published"
	// AlreadyPublished means an identical body was committed, even if the head moved.
	AlreadyPublished AdvanceOutcome = "already"
	// Conflict means this ID exists with a different body.
	Conflict AdvanceOutcome = "conflict"
	// Superseded means another run advanced the expected head.
	Superseded AdvanceOutcome = "superseded"
)

// ErrContinuationNotFound identifies a missing continuation in a session.
var ErrContinuationNotFound = errors.New("continuation not found")

// EncodeContinuation returns the deterministic body used for idempotency checks.
func EncodeContinuation(next Continuation) ([]byte, error) {
	return json.Marshal(next, json.Deterministic(true))
}
