package runtime

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type historyCountingClient struct {
	counts       []int
	countErr     error
	completeText string
	requests     []*model.Request
}

func (c *historyCountingClient) Complete(_ context.Context, _ *model.Request) (*model.Response, error) {
	return &model.Response{
		Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: c.completeText}},
		}},
	}, nil
}

func (c *historyCountingClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (c *historyCountingClient) CountTokens(_ context.Context, req *model.Request) (model.TokenCount, error) {
	c.requests = append(c.requests, req)
	if c.countErr != nil {
		return model.TokenCount{}, c.countErr
	}
	count := 0
	if len(c.counts) > 0 {
		count = c.counts[0]
		c.counts = c.counts[1:]
	}
	return model.TokenCount{InputTokens: count, Exact: true}, nil
}
