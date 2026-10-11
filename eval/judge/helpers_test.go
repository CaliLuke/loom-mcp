package judge

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type unusedClient struct{}

func (unusedClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	panic("unexpected Complete")
}

func (unusedClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	panic("unexpected Stream")
}
