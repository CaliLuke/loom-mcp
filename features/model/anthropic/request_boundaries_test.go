package anthropic

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicRejectsInvalidRequestsBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*model.Request)
		want string
	}{
		{"missing messages", func(r *model.Request) {
			r.Messages = nil
		}, "messages are required"},
		{"missing cap", func(r *model.Request) {
			r.MaxTokens = 0
		}, "max_tokens"},
		{"thinking without budget", func(r *model.Request) {
			r.Thinking = &model.ThinkingOptions{Enable: true}
		}, "thinking budget is required"},
		{"thinking too small", func(r *model.Request) {
			r.Thinking = &model.ThinkingOptions{Enable: true, BudgetTokens: 1023}
		}, ">= 1024"},
		{"thinking consumes cap", func(r *model.Request) {
			r.Thinking = &model.ThinkingOptions{Enable: true, BudgetTokens: 4096}
		}, "less than max_tokens"},
		{"system image", func(r *model.Request) {
			r.Messages[0] = &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}}}
		}, "unsupported system message part"},
		{"system checkpoint without content", func(r *model.Request) {
			r.Messages[0] = &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.CacheCheckpointPart{}}}
		}, "no preceding cacheable system block"},
		{"conversation checkpoint without content", func(r *model.Request) {
			r.Messages[0].Parts = []model.Part{model.CacheCheckpointPart{}}
		}, "no preceding cacheable conversation block"},
		{"assistant image", func(r *model.Request) {
			r.Messages[0] = &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}}}
		}, "only supported in user messages"},
		{"image format", func(r *model.Request) {
			r.Messages[0].Parts = []model.Part{model.ImagePart{Format: "invalid", Bytes: []byte("image")}}
		}, "unsupported image format"},
		{"image missing bytes", func(r *model.Request) {
			r.Messages[0].Parts = []model.Part{model.ImagePart{Format: model.ImageFormatPNG}}
		}, "image bytes are required"},
		{"unnamed tool use", func(r *model.Request) {
			r.Messages[0].Parts = []model.Part{model.ToolUsePart{ID: "call"}}
		}, "missing name"},
		{"tool result cannot encode", func(r *model.Request) {
			r.Messages[0].Parts = []model.Part{model.ToolResultPart{ToolUseID: "call", Content: make(chan int)}}
		}, "encode tool result"},
		{"tool missing description", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{{Name: "lookup"}}
		}, "missing description"},
		{"tool malformed schema", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{{Name: "lookup", Description: "Find", InputSchema: jsontext.Value(`{`)}}
		}, "schema"},
		{"choice without name", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool}
		}, "requires a tool name"},
		{"choice unknown tool", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "missing"}
		}, "does not match any tool"},
		{"choice unknown mode", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: "invalid"}
		}, "unsupported tool choice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := false
			stub := &stubMessagesClient{newFunc: func(context.Context) (*sdk.Message, error) {
				sent = true
				return nil, assert.AnError
			}}
			client, err := New(stub, Options{DefaultModel: "claude-sonnet"})
			require.NoError(t, err)
			req := &model.Request{MaxTokens: 4096, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
			tc.edit(req)
			_, err = client.Complete(t.Context(), req)
			require.ErrorContains(t, err, tc.want)
			stream, err := client.Stream(t.Context(), req)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, stream)
			assert.False(t, sent)
		})
	}
}

func TestAnthropicThinkingBudgetAndModelSelection(t *testing.T) {
	for _, tc := range []struct {
		name, explicit        string
		class                 model.ModelClass
		budget, defaultBudget int
		wantModel             string
		wantBudget            int64
	}{
		{"request budget", "", model.ModelClassHighReasoning, 2048, 1024, "large", 2048},
		{"default budget", "", model.ModelClassSmall, 0, 1024, "small", 1024},
		{"explicit model", "explicit", model.ModelClassHighReasoning, 1024, 0, "explicit", 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{Content: []sdk.ContentBlockUnion{{Type: "text", Text: "done"}}, StopReason: sdk.StopReasonEndTurn}}
			client, err := New(stub, Options{DefaultModel: "default", HighModel: "large", SmallModel: "small", MaxTokens: 4096, ThinkingBudget: int64(tc.defaultBudget)})
			require.NoError(t, err)
			response, err := client.Complete(t.Context(), &model.Request{Model: tc.explicit, ModelClass: tc.class, Thinking: &model.ThinkingOptions{Enable: true, BudgetTokens: tc.budget}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
			require.NoError(t, err)
			assert.Equal(t, "done", response.Content[0].Parts[0].(model.TextPart).Text)
			assert.Equal(t, tc.wantModel, stub.lastParams.Model)
			require.NotNil(t, stub.lastParams.Thinking.OfEnabled)
			assert.Equal(t, tc.wantBudget, stub.lastParams.Thinking.OfEnabled.BudgetTokens)
		})
	}
}
