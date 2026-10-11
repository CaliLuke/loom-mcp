package api

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// SealRecordInput fixes the terminal index and event identity before scheduling.
// Retries must receive the same pending entries, key and workflow timestamp.
type SealRecordInput struct {
	// ContextSettings snapshots resolved settings even before a first plan.
	ContextSettings compaction.ContextSettings
	// ContextRecovery is the resolved run configuration.
	ContextRecovery ContextRecovery
	// ContextCompaction preserves stamped settings even for a run with no adopted planner step.
	ContextCompaction *ContextCompaction `json:"context_compaction,omitzero"`
	// RootRunID is the shared content storage scope for the run tree.
	RootRunID string
	// RunID identifies the run whose transcript is sealed.
	RunID string
	// ProtocolVersion is copied from the workflow's validated RunInput.
	ProtocolVersion int
	// IndexHead is the most recently adopted index head.
	IndexHead runstore.Hash
	// Pending contains the remaining uncommitted transcript entries.
	Pending []index.MessageEntry
	// EventKey is the fixed logical identity run_sealed:<run>.
	EventKey string
	// Timestamp is fixed by the workflow clock before the first attempt.
	Timestamp time.Time
}

// SealRecordOutput identifies the terminal head after all pending entries commit.
type SealRecordOutput struct {
	// IndexHead addresses the complete sealed transcript.
	IndexHead runstore.Hash
}
