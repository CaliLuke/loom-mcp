package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type viewContent struct {
	store runstore.Store
	scope string
}

func (c viewContent) LoadToolResult(ctx context.Context, refs tools.ResultRefs) (rawjson.Message, error) {
	return c.load(ctx, refs.Result)
}

func (c viewContent) LoadServerData(ctx context.Context, refs tools.ResultRefs) (rawjson.Message, error) {
	return c.load(ctx, refs.ServerData)
}

func (c viewContent) LoadToolPayload(ctx context.Context, output *planner.ToolOutput) (rawjson.Message, error) {
	if output.Refs.Payload != "" {
		return c.load(ctx, output.Refs.Payload)
	}
	data, err := c.load(ctx, output.Call)
	if err != nil {
		return nil, err
	}
	var call planner.ToolRequest
	if err := json.Unmarshal(data, &call); err != nil {
		return nil, err
	}
	return call.Payload, nil
}

func (c viewContent) LoadTypedInput(ctx context.Context, hash runstore.Hash) (rawjson.Message, error) {
	return c.load(ctx, hash)
}

func (c viewContent) load(ctx context.Context, hash runstore.Hash) (rawjson.Message, error) {
	if hash == "" {
		return nil, fmt.Errorf("load content: %w: empty hash", runstore.ErrNotFound)
	}
	data, err := c.store.Get(ctx, c.scope, hash)
	return rawjson.Message(data), err
}
