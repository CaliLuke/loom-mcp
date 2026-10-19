package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// callInfoClientWrapper allocates identity before invoking any model middleware.
// Its base identity belongs to one planner activity; each invocation copies it.
type callInfoClientWrapper struct {
	inner     model.Client
	store     runstore.Store
	info      model.CallInfo
	recording *CallRecording
}

func (c *callInfoClientWrapper) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	ctx, err := c.callContext(ctx)
	if err != nil {
		return nil, err
	}
	return c.inner.Complete(ctx, req)
}

func (c *callInfoClientWrapper) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	ctx, err := c.callContext(ctx)
	if err != nil {
		return nil, err
	}
	return c.inner.Stream(ctx, req)
}

// callContext consumes a fresh sequence even if the subsequent call fails.
func (c *callInfoClientWrapper) callContext(ctx context.Context) (context.Context, error) {
	if c.recording != nil && c.recording.maxModelCalls < 0 {
		return nil, fmt.Errorf("%w: MaxModelCalls must not be negative", ErrInvalidConfig)
	}
	info := c.info
	seq, err := c.store.NextSeq(ctx, info.RootRunID, info.RunID)
	if err != nil {
		failure := fmt.Errorf("allocate model call sequence: %w", err)
		if c.recording != nil {
			c.recording.recordAllocationFailure(failure)
		}
		return nil, failure
	}
	info.Seq = seq
	if c.recording != nil {
		if err := c.recording.admitModelCall(info); err != nil {
			return nil, err
		}
	}
	return model.WithCallInfo(ctx, info), nil
}
