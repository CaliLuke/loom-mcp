package summaryeval

import (
	"context"

	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
)

func oldSide(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput) (Side, error) {
	side := c.Old
	rendered, err := checkpoint.FromRecord(ctx, c.source, c.Record, c.run.Entries, in.ViewRef.ChainPos)
	if err != nil {
		return side, err
	}
	if c.summary.ViewRef != nil {
		rendered.Pinned = c.summary.ViewRef.Pinned
	}
	message, err := checkpoint.Render(rendered, in.Budget, checkpoint.BudgetLimits(in.Budget), nil)
	if err != nil {
		return side, err
	}
	side.RenderedBytes = len(messageText(message))
	return side, nil
}
