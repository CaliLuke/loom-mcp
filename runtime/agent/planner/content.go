package planner

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ContentLoader reads full content in the root scope. Planner tool outputs and
// typed inputs carry references rather than inline payload/result/server bytes.
type ContentLoader interface {
	LoadToolResult(context.Context, tools.ResultRefs) (rawjson.Message, error)
	LoadServerData(context.Context, tools.ResultRefs) (rawjson.Message, error)
	LoadToolPayload(context.Context, *ToolOutput) (rawjson.Message, error)
	LoadTypedInput(context.Context, runstore.Hash) (rawjson.Message, error)
}
