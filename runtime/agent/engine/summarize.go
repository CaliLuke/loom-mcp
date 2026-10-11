package engine

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"

// SummarizeContextActivityCall schedules a recorded summary at an immutable cut.
type SummarizeContextActivityCall struct {
	Name    string
	Input   *api.SummarizeContextInput
	Options ActivityOptions
}
