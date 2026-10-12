package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// SummarizeContextInput binds one synchronous summary to an immutable cut.
	SummarizeContextInput struct {
		ContextSettings                 compaction.Settings
		ContextFingerprint              string
		AgentID                         agent.Ident
		RunID, Scope, SessionID, TurnID string
		Labels                          map[string]string
		Step                            int
		ViewRef                         index.ViewRef
		From, To                        index.MessageID
		Budget                          index.RenderBudget
		Rate                            contextmeasure.Rate
		ContextModel                    ContextModel
		PromptID                        prompt.Ident
		Settings                        SummarySettings
	}
	// SummarizeContextOutput exposes only durable addresses and bounded metadata.
	// CallKeys includes every started call, independently of checkpoint Calls.
	SummarizeContextOutput struct {
		// Latency measures the whole summary activity attempt in nanoseconds.
		Latency                              int64
		From, To                             index.MessageID
		IndexHead                            runstore.Hash
		Checkpoint                           *runstore.Hash
		Pinned                               []index.MessageID
		Kind                                 string
		CallKeys                             []runstore.CallKey
		Facts                                []contextmeasure.RequestFacts
		Prompt                               *prompt.PromptRef
		Usage                                model.TokenUsage
		Chunks, UnsummarisedBytes, LooseRefs int
	}
)
