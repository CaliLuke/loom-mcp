package summaryeval

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Cut identifies an immutable recorded eviction interval, [From, To).
	Cut struct {
		RunID        string          `json:"run_id"`
		Step         int             `json:"step"`
		From         index.MessageID `json:"from"`
		To           index.MessageID `json:"to"`
		Origin       string          `json:"origin"`
		Record       runstore.Hash   `json:"record"`
		Prev         *runstore.Hash  `json:"prev"`
		ChainPos     int             `json:"chain_pos"`
		EvictedBytes int             `json:"evicted_bytes"`
		Old          Side            `json:"-"`
		record       *runrecord.RunRecord
		run          *runrecord.Run
		event        *runlog.Event
		summary      hooks.ContextCompactionEvent
		eventIndex   int
	}
	// Side preserves a summary and its measured production output.
	Side struct {
		Kind              string            `json:"kind"`
		Text              string            `json:"text"`
		Bytes             int               `json:"bytes"`
		RenderedBytes     int               `json:"rendered_bytes"`
		Usage             model.TokenUsage  `json:"usage"`
		Chunks            int               `json:"chunks"`
		UnsummarisedBytes int               `json:"unsummarised_bytes"`
		LooseRefs         int               `json:"loose_refs"`
		Latency           time.Duration     `json:"latency"`
		Prompt            *prompt.PromptRef `json:"prompt"`
		Fingerprint       string            `json:"fingerprint"`
		Checkpoint        runstore.Hash     `json:"checkpoint"`
		Calls             []Call            `json:"calls"`
		Cached            bool              `json:"cached"`
	}
	// Call is the inspectable evidence of a scratch summarizer invocation.
	Call struct {
		Seq    int           `json:"seq"`
		Digest runstore.Hash `json:"digest"`
		Text   string        `json:"text"`
	}
	// Verification distinguishes reproducible baselines from mismatches and missing evidence.
	Verification struct {
		Status    string `json:"status"`
		FirstCall int    `json:"first_call,omitzero"`
		Field     string `json:"field,omitzero"`
		Reason    string `json:"reason,omitzero"`
	}
)
