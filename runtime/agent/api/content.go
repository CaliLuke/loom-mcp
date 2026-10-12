package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// RecordContentInput carries ordered content to store before workflow adoption.
type RecordContentInput struct {
	// Continue requests recorded session seeding for a compaction root.
	Continue *ContinuationStartInput `json:"continue,omitzero"`
	// ContextStart pins child context settings outside workflow execution.
	ContextStart *ContextStartInput `json:"context_start,omitzero"`
	// RootRunID is the shared storage scope of the run tree.
	RootRunID string
	// Items contains the exact bytes to store, in order.
	Items [][]byte
	// SeedMessages stores Items as caller messages and commits their first index segments.
	SeedMessages bool
	// Typed validates a referenced answer without returning its content.
	Typed *TypedContentValidation
	// Clarification joins an await-only transcript and its question in one assistant turn.
	Clarification *ClarificationContent
	// Provided materializes external results without exposing signal content to workflows.
	Provided *ProvidedContent
	// FinalizeCalls canonicalizes and stores a fixed terminal plan before dispatch.
	FinalizeCalls *FinalizationContent
}

// TypedContentValidation validates one stored answer against its awaited schema.
type TypedContentValidation struct {
	Schema     rawjson.Message
	PayloadRef runstore.Hash
}

// ClarificationContent preserves streamed text before an awaited question.
type ClarificationContent struct {
	TranscriptRef runstore.Hash
	Question      string
}

// ProvidedContent carries only awaited call metadata and signal references.
type ProvidedContent struct {
	// TimestampMS is workflow time at consumption, stable across activity retries.
	TimestampMS int64
	RunContext  run.Context
	AgentID     agent.Ident
	Calls       []planner.ToolRequest
	Results     []*ProvidedToolResult
	First       index.MessageID
	CallRefs    map[string]runstore.Hash
}

// RecordContentOutput is returned only after every input item has been stored.
type RecordContentOutput struct {
	// Continuation records start lineage and the selected view.
	Continuation *ContinuationStartOutput `json:"continuation,omitzero"`
	// ContextStart is the recorded result of child prompt and settings pinning.
	ContextStart *ContextStartOutput `json:"context_start,omitzero"`
	// Refs addresses Items in the same order and with the same length.
	Refs        []runstore.Hash
	IndexHead   runstore.Hash
	Entries     []index.MessageEntry
	ToolResults []*ToolEvent
	Calls       []planner.ToolRequest
	CallRefs    []runstore.Hash
}

// QueryRootRunID is the workflow query used by content-producing signal clients.
const QueryRootRunID = "root_run_id"

// PublishToolContent supplies references and metadata for a tool result hook.
type PublishToolContent struct {
	AgentID    agent.Ident
	RunContext run.Context
	Call       planner.ToolRequest
	Result     *ToolEvent
	Duration   int64
}

// FinalizationContent carries one terminal batch and its bounded policy context.
type FinalizationContent struct {
	Input PlanActivityInput
	Calls []planner.ToolRequest
}

// ContextStartInput resolves a child prompt using its own registered settings.
type ContextStartInput struct {
	AgentID   agent.Ident
	SessionID string
	Labels    map[string]string
	Settings  compaction.Settings
}

// ContextStartOutput contains the immutable settings and their stored identity.
type ContextStartOutput struct {
	Settings    compaction.Settings
	Fingerprint string
}
