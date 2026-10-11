package bedrock

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

func TestContextLimitsUnknownTable(t *testing.T) {
	c := &Client{defaultModel: "default", highModel: "high", smallModel: "small", maxTok: 128}
	for _, tc := range []struct {
		req model.Request
		id  string
	}{
		{model.Request{}, "default"},
		{model.Request{ModelClass: model.ModelClassHighReasoning}, "high"},
		{model.Request{ModelClass: model.ModelClassSmall}, "small"},
		{model.Request{Model: "explicit", ModelClass: model.ModelClassSmall}, "explicit"},
	} {
		limits, resolved, known := c.ContextLimits(context.Background(), &tc.req)
		assert.False(t, known)
		assert.Equal(t, tc.id, resolved)
		assert.Equal(t, model.ContextLimits{DefaultMaxTokens: 128}, limits)
	}
	c.maxTok = -1
	limits, _, known := c.ContextLimits(context.Background(), &model.Request{})
	assert.False(t, known)
	assert.Zero(t, limits.DefaultMaxTokens)
}
