package bedrock

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestEncodeTools_NoChoice(t *testing.T) {
	ctx := context.Background()

	cfg, canonToSan, sanToCanon, err := encodeTools(ctx, []*model.ToolDefinition{
		{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		},
	}, nil, false, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 1)
	require.Nil(t, cfg.ToolChoice)
	require.Len(t, canonToSan, 1)
	require.Len(t, sanToCanon, 1)
}

func TestEncodeTools_ModeAny(t *testing.T) {
	ctx := context.Background()

	cfg, canonToSan, sanToCanon, err := encodeTools(ctx, []*model.ToolDefinition{
		{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		},
	}, &model.ToolChoice{Mode: model.ToolChoiceModeAny}, false, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 1)
	require.Len(t, canonToSan, 1)
	require.Len(t, sanToCanon, 1)
	choice, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberAny)
	require.True(t, ok, "expected ToolChoiceMemberAny")
	require.NotNil(t, choice)
}

func TestEncodeTools_ModeTool(t *testing.T) {
	ctx := context.Background()

	cfg, canonToSan, sanToCanon, err := encodeTools(ctx, []*model.ToolDefinition{
		{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		},
	}, &model.ToolChoice{
		Mode: model.ToolChoiceModeTool,
		Name: "lookup",
	}, false, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 1)
	require.Len(t, canonToSan, 1)
	require.Len(t, sanToCanon, 1)
	member, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberTool)
	require.True(t, ok, "expected ToolChoiceMemberTool")
	require.NotNil(t, member)
	require.NotNil(t, member.Value.Name)
	require.Equal(t, "lookup", sanToCanon[*member.Value.Name])
}

func TestEncodeTools_ModeNonePreservesConfig(t *testing.T) {
	ctx := context.Background()

	cfg, canonToSan, sanToCanon, err := encodeTools(ctx, []*model.ToolDefinition{
		{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		},
	}, &model.ToolChoice{Mode: model.ToolChoiceModeNone}, false, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 1)
	require.Nil(t, cfg.ToolChoice)
	require.Len(t, canonToSan, 1)
	require.Len(t, sanToCanon, 1)
}

func TestEncodeTools_ChoiceWithoutToolsErrors(t *testing.T) {
	ctx := context.Background()

	_, _, _, err := encodeTools(ctx, nil, &model.ToolChoice{Mode: model.ToolChoiceModeAny}, false, nil)
	require.Error(t, err)
}

func TestEncodeTools_AppendsCacheCheckpoint(t *testing.T) {
	ctx := context.Background()

	cfg, _, _, err := encodeTools(ctx, []*model.ToolDefinition{
		{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		},
	}, nil, true, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 2, "expected tool spec + cache checkpoint")
	_, ok := cfg.Tools[1].(*brtypes.ToolMemberCachePoint)
	require.True(t, ok, "expected second tool entry to be cache checkpoint")
}

func TestIsNovaModel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "claude", in: "anthropic.claude-3-sonnet-20241022-v1:0", want: false},
		{name: "nova", in: "amazon.nova-pro-v1:0", want: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := isNovaModel(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsAdaptiveThinkingModel_MatchesOpus47Scopes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "opus 4.6", in: "global.anthropic.claude-opus-4-6-v1:0", want: true},
		{name: "opus 4.7 base", in: "anthropic.claude-opus-4-7-v1:0", want: true},
		{name: "opus 4.7 us", in: "us.anthropic.claude-opus-4-7-v1:0", want: true},
		{name: "opus 4.7 eu", in: "eu.anthropic.claude-opus-4-7-v1:0", want: true},
		{name: "opus 4.7 jp", in: "jp.anthropic.claude-opus-4-7-v1:0", want: true},
		{name: "opus 4.7 global", in: "global.anthropic.claude-opus-4-7-v1:0", want: true},
		{name: "sonnet", in: "us.anthropic.claude-sonnet-4-20250514-v1:0", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := isAdaptiveThinkingModel(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveThinking_AdaptiveModelDoesNotRequireTools(t *testing.T) {
	client := &Client{think: defaultThinkingBudget}
	req := &model.Request{
		Thinking: &model.ThinkingOptions{Enable: true},
	}
	parts := &requestParts{
		modelID: "global.anthropic.claude-opus-4-7-v1:0",
	}

	got := client.resolveThinking(req, parts)

	require.True(t, got.enable)
	require.True(t, got.adaptive)
	require.False(t, got.interleaved)
	require.Zero(t, got.budget)
}

func TestBuildConverseStreamInput_AdaptiveThinkingRequestsSummaries(t *testing.T) {
	client := &Client{
		maxTok: 1024,
		temp:   0.7,
		think:  defaultThinkingBudget,
	}
	req := &model.Request{
		Thinking: &model.ThinkingOptions{Enable: true},
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.TextPart{Text: "hello"},
			},
		}},
	}
	parts := &requestParts{
		modelID: "global.anthropic.claude-opus-4-7-v1:0",
		messages: []brtypes.Message{{
			Role: brtypes.ConversationRoleUser,
		}},
	}
	thinking := client.resolveThinking(req, parts)

	input := client.buildConverseStreamInput(parts, req, thinking)

	require.NotNil(t, input.AdditionalModelRequestFields)
	raw, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	thinkingField, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "adaptive", thinkingField["type"])
	require.Equal(t, "summarized", thinkingField["display"])
}

func TestInferenceConfig_OmitsTemperatureForOpus47(t *testing.T) {
	client := &Client{
		maxTok: 2048,
		temp:   0.7,
	}

	opus := client.inferenceConfig("us.anthropic.claude-opus-4-7-v1:0", 0, 0)
	require.NotNil(t, opus)
	require.NotNil(t, opus.MaxTokens)
	require.Nil(t, opus.Temperature)

	sonnet := client.inferenceConfig("us.anthropic.claude-sonnet-4-20250514-v1:0", 0, 0)
	require.NotNil(t, sonnet)
	require.NotNil(t, sonnet.MaxTokens)
	require.NotNil(t, sonnet.Temperature)
	require.InEpsilon(t, float32(0.7), *sonnet.Temperature, 0.0001)
}

func TestSanitizeToolName_StripsNamespaces(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "ada toolset preserves namespace",
			in:   "ada.get_application_status",
			want: "ada_get_application_status",
		},
		{
			name: "ada time series preserves namespace",
			in:   "ada.get_time_series",
			want: "ada_get_time_series",
		},
		{
			name: "chat atlas read subset preserves full canonical id",
			in:   "atlas.read.chat.chat_get_user_details",
			want: "atlas_read_chat_chat_get_user_details",
		},
		{
			name: "chat emit toolset preserves namespace",
			in:   "chat.emit.ask_clarifying_question",
			want: "chat_emit_ask_clarifying_question",
		},
		{
			name: "todos toolset preserves namespace",
			in:   "todos.todos.update_todos",
			want: "todos_todos_update_todos",
		},
		{
			name: "plain name passthrough",
			in:   "lookup",
			want: "lookup",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeToolName(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeToolName_NoCollisionsAcrossToolsets(t *testing.T) {
	a := SanitizeToolName("atlas.read.explain_control_logic")
	b := SanitizeToolName("ada.explain_control_logic")

	require.NotEmpty(t, a)
	require.NotEmpty(t, b)
	require.NotEqual(t, a, b)
}

func TestSanitizeToolName_TruncatesWithStableHashSuffix(t *testing.T) {
	in := "atlas.read.chat." + strings.Repeat("very_long_segment_", 10) + "tool"
	got := SanitizeToolName(in)

	require.NotEmpty(t, got)
	require.LessOrEqual(t, len(got), 64)
	require.Regexp(t, `_[0-9a-f]{8}$`, got)
	require.Equal(t, got, SanitizeToolName(in))
}
