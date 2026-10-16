package bedrock

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockDocumentNamesRemainStableAndDistinct(t *testing.T) {
	provider := &errorRuntimeClient{}
	client := &Client{runtime: provider, defaultModel: "anthropic.claude-sonnet-4-20250514-v1:0", logger: telemetry.NewNoopLogger()}
	request := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{
		model.TextPart{Text: "read these"},
		model.DocumentPart{Name: "a!", Format: model.DocumentFormatPDF, Bytes: []byte("one")},
		model.DocumentPart{Name: "a?", Format: model.DocumentFormatPDF, Bytes: []byte("two")},
		model.DocumentPart{Name: "a!", Format: model.DocumentFormatPDF, Bytes: []byte("one")},
		model.DocumentPart{Name: "!!!", Format: model.DocumentFormatPDF, Bytes: []byte("three")},
		model.DocumentPart{Name: "x  y-[z](v)", Format: model.DocumentFormatPDF, Bytes: []byte("four")},
	}}}}
	_, err := client.Complete(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, provider.converseInput)
	var names []string
	for _, block := range provider.converseInput.Messages[0].Content {
		if doc, ok := block.(*brtypes.ContentBlockMemberDocument); ok {
			names = append(names, *doc.Value.Name)
		}
	}
	assert.Equal(t, []string{"a", "a (1)", "a", "document", "x y-[z](v)"}, names)
}

func TestBedrockLegacyStreamingThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		budget, defaultBudget int32
		tools, interleaved    bool
		want                  string
	}{
		{"explicit", 2048, 1024, true, true, `{"thinking":{"type":"enabled","budget_tokens":2048},"anthropic_beta":["interleaved-thinking-2025-05-14"]}`},
		{"default", 0, 1024, true, false, `{"thinking":{"type":"enabled","budget_tokens":1024}}`},
		{"no budget", 0, 0, true, false, `{"thinking":{"type":"enabled"}}`},
		{"no tools", 2048, 1024, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &errorRuntimeClient{}
			client := &Client{runtime: provider, defaultModel: "anthropic.claude-sonnet-4-20250514-v1:0", maxTok: 4096, think: int(tc.defaultBudget), logger: telemetry.NewNoopLogger()}
			request := &model.Request{Temperature: 0.3, Thinking: &model.ThinkingOptions{Enable: true, BudgetTokens: int(tc.budget), Interleaved: tc.interleaved}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
			if tc.tools {
				request.Tools = []*model.ToolDefinition{{Name: "lookup", Description: "Lookup", InputSchema: map[string]any{"type": "object"}}}
			}
			client.converseStream = func(_ context.Context, input *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (StreamOutput, error) {
				provider.converseStreamInput = input
				return nil, assert.AnError
			}
			_, err := client.Complete(t.Context(), request)
			require.NoError(t, err)
			_, err = client.Stream(t.Context(), request)
			require.ErrorIs(t, err, assert.AnError)
			require.NotNil(t, provider.converseInput)
			require.NotNil(t, provider.converseStreamInput)
			for _, fields := range []interface{ MarshalSmithyDocument() ([]byte, error) }{provider.converseStreamInput.AdditionalModelRequestFields} {
				if tc.want == "" {
					assert.Nil(t, fields)
					continue
				}
				require.NotNil(t, fields)
				raw, err := fields.MarshalSmithyDocument()
				require.NoError(t, err)
				assert.JSONEq(t, tc.want, string(raw))
			}
			assert.InDelta(t, 0.3, *provider.converseInput.InferenceConfig.Temperature, 0.0001)
		})
	}
}
