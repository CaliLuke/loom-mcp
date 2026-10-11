// Package index defines immutable transcript entries and their model-facing
// projection. It performs no workflow, planner, or store writes. Callers bind a
// Source to the authorized root run scope before rendering or walking segments.
package index

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// MessageID is the one-based position of an entry within a run.
	MessageID uint64

	// EntryKind identifies the encoding and rendering operation of an entry.
	EntryKind string

	// StructuralKind identifies a workflow-authored operation or template.
	StructuralKind string

	// Source reads exact stored bytes by hash within one root run scope.
	Source func(context.Context, runstore.Hash) ([]byte, error)

	// TextParameter holds either inline UTF-8 text (at most 16 KiB) or a hash of
	// the complete text bytes. Writers must use RecordContent for larger text.
	TextParameter struct {
		Text string        `json:"text,omitzero"`
		Hash runstore.Hash `json:"hash,omitzero"`
	}

	// ResultProjection contains only metadata needed by the existing transcript
	// projection. A non-nil Error preserves IsError even for empty error text.
	ResultProjection struct {
		Preview TextParameter  `json:"preview,omitzero"`
		Bounds  *agent.Bounds  `json:"bounds,omitzero"`
		Error   *TextParameter `json:"error,omitzero"`
	}

	// StructuralEntry is a versioned rendering operation. Text contains template
	// arguments (or a denial's canonical JSON), never unbounded inline content.
	// Reason selects a finalize hint. IDs and batch membership live on MessageEntry.
	StructuralEntry struct {
		Kind            StructuralKind  `json:"kind"`
		ProtocolVersion int             `json:"protocol_version"`
		Text            []TextParameter `json:"text,omitzero"`
		Reason          string          `json:"reason,omitzero"`
	}

	// MessageEntry records one immutable operation in transcript order. Hash
	// addresses one model.Message, an assistant transcript slice, canonical result
	// JSON, raw clarification text, or typed input JSON according to Kind.
	// Calls addresses the exact dispatched tool requests, in dispatch order.
	// Adjacent results with the same nonzero Batch form one user message; Batch
	// is the first result entry's ID. Record is server-only and never rendered.
	MessageEntry struct {
		ID    MessageID       `json:"id"`
		Kind  EntryKind       `json:"kind"`
		Hash  runstore.Hash   `json:"hash,omitzero"`
		Calls []runstore.Hash `json:"calls,omitzero"`
		// Call identifies the completed model invocation that produced this assistant entry.
		Call       *runstore.CallKey      `json:"call,omitzero"`
		Structural *StructuralEntry       `json:"structural,omitzero"`
		Role       model.ConversationRole `json:"role,omitzero"`
		ToolUseIDs []string               `json:"tool_use_ids,omitzero"`
		Bytes      int                    `json:"bytes,omitzero"`
		NonText    int                    `json:"non_text,omitzero"`
		Record     []runstore.Hash        `json:"record,omitzero"`
		Keep       bool                   `json:"keep,omitzero"`
		Origin     *Origin                `json:"origin,omitzero"`
		Batch      MessageID              `json:"batch,omitzero"`
		Result     *ResultProjection      `json:"result,omitzero"`
		InputID    string                 `json:"input_id,omitzero"`
		// Output addresses ref-only planner tool-output metadata. It is not model content.
		Output runstore.Hash `json:"output,omitzero"`
		Chain  runstore.Hash `json:"chain,omitzero"`
	}

	// Origin reserves continuation provenance for K18; it is nil in K7.
	Origin struct {
		Scope string    `json:"scope"`
		Run   string    `json:"run"`
		Pos   int       `json:"pos"`
		ID    MessageID `json:"id"`
		Form  string    `json:"form"`
	}

	// RenderBudget reserves token budgets for later compaction tickets.
	RenderBudget struct {
		KeepUserTokens   int `json:"keep_user_tokens,omitzero"`
		MaxSummaryTokens int `json:"max_summary_tokens,omitzero"`
		ReceiptTokens    int `json:"receipt_tokens,omitzero"`
		FramingTokens    int `json:"framing_tokens,omitzero"`
		PreviewTokens    int `json:"preview_tokens,omitzero"`
	}

	// ViewRef names the committed index and one step's pending entries. All
	// fields after Pending are reserved and must remain zero during K7.
	ViewRef struct {
		IndexHead      runstore.Hash  `json:"index_head,omitzero"`
		Pending        []MessageEntry `json:"pending,omitzero"`
		Checkpoint     *runstore.Hash `json:"checkpoint,omitzero"`
		TailFrom       MessageID      `json:"tail_from,omitzero"`
		ClearedThrough MessageID      `json:"cleared_through,omitzero"`
		Pinned         []MessageID    `json:"pinned,omitzero"`
		Budget         RenderBudget   `json:"budget,omitzero"`
	}

	// IndexSegment contains a nonempty contiguous slice of immutable entries.
	// Prev is nil only for the first segment, whose First must be 1.
	IndexSegment struct {
		Prev    *runstore.Hash `json:"prev,omitzero"`
		First   MessageID      `json:"first"`
		Entries []MessageEntry `json:"entries"`
	}
)

const (
	// Message appends one stored caller or resume message verbatim.
	Message EntryKind = "message"
	// Assistant merges one transcript slice with its dispatched call batch.
	Assistant EntryKind = "assistant"
	// ToolResult projects one result into its batch's user message.
	ToolResult EntryKind = "tool_result"
	// Clarification appends nonempty raw answer text as a user message.
	Clarification EntryKind = "clarification"
	// TypedInput records an answer without producing model messages.
	TypedInput EntryKind = "typed_input"
	// Structural applies a versioned workflow-authored rendering operation.
	Structural EntryKind = "structural"

	// UnknownTool produces an unknown-tool error from one tool-name parameter.
	UnknownTool StructuralKind = "unknown_tool"
	// PolicyDenial records a policy rejection; without IDs it produces no message.
	PolicyDenial StructuralKind = "policy_denial"
	// ConfirmationDenial projects the schema-compliant JSON in its text parameter.
	ConfirmationDenial StructuralKind = "confirmation_denial"
	// SynthesizedError projects an error from one text parameter.
	SynthesizedError StructuralKind = "synthesized_error"
	// Cancellation produces the time-budget cancellation result.
	Cancellation StructuralKind = "cancellation"
	// FinalizeResults closes calls in the last assistant message with errors.
	FinalizeResults StructuralKind = "finalize_results"
	// FinalizeHint appends the system guidance selected by Reason.
	FinalizeHint StructuralKind = "finalize_hint"
	// Reminders joins its text parameters into one system message per batch.
	Reminders StructuralKind = "reminders"
	// HideBookkeeping removes named calls from earlier assistant messages and
	// omits their following results. Stored entries and segment hashes never change.
	HideBookkeeping StructuralKind = "hide_bookkeeping"

	// TemplateProtocolVersion versions the structural templates for protocol 5.
	TemplateProtocolVersion = 5
	// MaxInlineTextBytes is the inline structural parameter bound.
	MaxInlineTextBytes = 16 * 1024
)
