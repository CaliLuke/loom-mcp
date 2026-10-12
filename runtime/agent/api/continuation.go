package api

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// ContinuationStartInput enables continuation only for a compaction root.
type ContinuationStartInput struct {
	ContextFingerprint string
	SessionID          string
	RunID              string
	AgentID            agent.Ident
	Settings           compaction.Settings
	Timestamp          time.Time
}

// ContinuationStartOutput records the selected predecessor and initial view.
type ContinuationStartOutput struct {
	Prev          *string
	Pos           int
	ContinuedFrom *index.RunCheckpoint
	Outcome       string
	Reason        string
	View          index.ViewRef
	Limits        model.ContextLimits
	Visible       []runstore.Hash
}
