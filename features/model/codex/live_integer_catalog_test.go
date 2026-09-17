package codex_test

import (
	"context"
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

// testLiveCodexIntegerCatalog exercises the generated mixed catalog through the
// validated client, including a model tool call and its result continuation.
func testLiveCodexIntegerCatalog(t *testing.T, ctx context.Context, provider *codex.Client) {
	t.Helper()
	data, err := os.ReadFile("../../../integration_tests/fixtures/assistant/gen/assistant/agents/assistant_runtime/specs/tool_schemas.json")
	require.NoError(t, err)
	var catalog struct {
		Tools []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Payload     struct {
				Schema rawjson.Message `json:"schema"`
			} `json:"payload"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(data, &catalog))
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	request := &model.Request{
		Messages:   []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "Reply with exactly OK. Do not call any tools."}}}},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeNone},
	}
	const toolName = "projected.projected_lookup_tool"
	for _, tool := range catalog.Tools {
		request.Tools = append(request.Tools, &model.ToolDefinition{Name: tool.ID, Description: tool.Description, InputSchema: tool.Payload.Schema})
		if tool.ID == toolName {
			assert.Contains(t, string(tool.Payload.Schema), "9223372036854775807")
			assert.Contains(t, string(tool.Payload.Schema), "18446744073709551615")
		}
	}
	require.Greater(t, len(request.Tools), 1)
	response, err := client.Complete(ctx, request)
	require.NoError(t, err)
	require.NotEmpty(t, response.Content)
	request.Messages = []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "Call projected.projected_lookup_tool with query codex, count 7, signed -7, and unsigned 7. Then report the result."}}}}
	request.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: toolName}
	response, err = client.Complete(ctx, request)
	require.NoError(t, err)
	require.Len(t, response.ToolCalls, 1)
	call := response.ToolCalls[0]
	assert.Equal(t, toolName, string(call.Name))
	request.Messages = append(request.Messages,
		&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: call.ID, Name: string(call.Name), Input: call.Payload}}},
		&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: call.ID, Content: map[string]any{"answer": "integer catalog ok", "source": "test"}}}},
	)
	request.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeNone}
	response, err = client.Complete(ctx, request)
	require.NoError(t, err)
	require.NotEmpty(t, response.Content)
}
