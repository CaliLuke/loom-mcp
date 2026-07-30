package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func (c *historyCountingClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
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

func TestCompressTriggersAtMaxInputTokensAndKeepsExactTail(t *testing.T) {
	client := &historyCountingClient{
		counts:       []int{120, 30, 56},
		completeText: "summary",
	}
	policy := Compress(client, HistoryCompressionConfig{
		CompressAtMaxInputTokens: 100,
		KeepMaxInputTokens:       25,
	})
	msgs := historyMessages("one", "two", "three")
	tools := []*model.ToolDefinition{{
		Name:        "lookup",
		Description: "Lookup",
		InputSchema: map[string]any{"type": "object"},
	}}

	got, err := policy(context.Background(), msgs, tools)
	require.NoError(t, err)

	require.Len(t, got, 4)
	assert.Equal(t, model.ConversationRoleSystem, got[0].Role)
	assert.Equal(t, model.ConversationRoleSystem, got[1].Role)
	assert.Equal(t, "[Conversation Summary]\nsummary", got[1].Parts[0].(model.TextPart).Text)
	assert.Equal(t, "user three", got[2].Parts[0].(model.TextPart).Text)
	assert.Equal(t, "assistant three", got[3].Parts[0].(model.TextPart).Text)
	require.NotEmpty(t, client.requests)
	require.Len(t, client.requests[0].Tools, 2)
	assert.Equal(t, "lookup", client.requests[0].Tools[0].Name)
	assert.Equal(t, "runtime.tool_unavailable", client.requests[0].Tools[1].Name)
}

func TestCompressRequiresExactTokenCounts(t *testing.T) {
	client := &historyCountingClient{}
	policy := Compress(client, HistoryCompressionConfig{
		CompressAtMaxInputTokens: 10,
		KeepMaxTurns:             1,
	}, WithTokenCounter(model.TokenEstimator{}))

	_, err := policy(context.Background(), historyMessages("one", "two"), nil)

	require.ErrorContains(t, err, "requires exact token counts")
}

func TestCompressPropagatesTokenCountError(t *testing.T) {
	client := &historyCountingClient{countErr: errors.New("count failed")}
	policy := Compress(client, HistoryCompressionConfig{
		CompressAtMaxInputTokens: 10,
		KeepMaxTurns:             1,
	})

	_, err := policy(context.Background(), historyMessages("one", "two"), nil)

	require.ErrorContains(t, err, "count failed")
}

func historyMessages(labels ...string) []*model.Message {
	msgs := make([]*model.Message, 0, 1+2*len(labels))
	msgs = append(msgs, &model.Message{
		Role:  model.ConversationRoleSystem,
		Parts: []model.Part{model.TextPart{Text: "system"}},
	})
	for _, label := range labels {
		msgs = append(msgs,
			&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "user " + label}}},
			&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "assistant " + label}}},
		)
	}
	return msgs
}
