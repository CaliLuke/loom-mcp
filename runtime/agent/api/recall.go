package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type (
	// ContextRecovery configures context lookup independently of threshold compaction.
	// K14 extends this settings contract with overflow recovery controls.
	ContextRecovery = run.ContextRecovery
	// RecallActivityInput carries trusted workflow evidence and untrusted tool arguments.
	RecallActivityInput struct {
		RunID     string
		Scope     string
		ViewRef   index.ViewRef
		Head      runstore.Hash
		Call      planner.ToolRequest
		PageBytes int
		ScanBytes int
	}
	// RecallActivityOutput addresses the stored eligible result and its event classification.
	RecallActivityOutput struct {
		Result *ToolOutput
		Code   recall.Code
		Bytes  int
	}
)
