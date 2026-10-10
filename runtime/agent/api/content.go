package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// RecordContentInput carries ordered content to store before workflow adoption.
type RecordContentInput struct {
	// RootRunID is the shared storage scope of the run tree.
	RootRunID string
	// Items contains the exact bytes to store, in order.
	Items [][]byte
	// SeedMessages stores Items as caller messages and commits their first index segments.
	SeedMessages bool
	// Typed validates a referenced answer without returning its content.
	Typed *TypedContentValidation
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

// ProvidedContent carries only awaited call metadata and signal references.
type ProvidedContent struct {
	RunContext run.Context
	AgentID    agent.Ident
	Calls      []planner.ToolRequest
	Results    []*ProvidedToolResult
	First      index.MessageID
	CallRefs   map[string]runstore.Hash
}

// RecordContentOutput is returned only after every input item has been stored.
type RecordContentOutput struct {
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
