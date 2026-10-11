package contextmeasure

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeasureNonText(t *testing.T) {
	for _, imageTokens := range []int{0, 700} {
		t.Run(fmt.Sprint(imageTokens), func(t *testing.T) {
			req := &model.Request{Model: "resolved", Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{
				model.ImagePart{Format: model.ImageFormatPNG, Bytes: make([]byte, 12000)},
				model.DocumentPart{Name: "pdf", Format: model.DocumentFormatPDF, Bytes: make([]byte, 101)},
				model.DocumentPart{Name: "txt", Format: model.DocumentFormatTXT, Text: "abcdef"},
				model.DocumentPart{Name: "uri", Format: model.DocumentFormatPDF, URI: "s3://bucket/document.pdf"},
			}}}}
			facts, err := Measure(req, model.ContextLimits{ImageTokens: imageTokens}, nil)
			require.NoError(t, err)
			cost := imageTokens
			if cost == 0 {
				cost = 1500
			}
			assert.Equal(t, 2*cost+51, facts.NonText)
			assert.Equal(t, 15, facts.TextBytes)
			assert.Zero(t, facts.FixedBytes)
			facts.InputTokens = facts.NonText + 5
			rate, calibrated := Calibrate(facts)
			assert.True(t, calibrated)
			assert.Equal(t, facts.InputTokens, Estimate(rate, facts.FixedBytes, facts.TextBytes-facts.FixedBytes, facts.NonText), "non-text counted twice: %+v, %+v", facts, rate)
		})
	}
}

func TestMeasureTextAndFixed(t *testing.T) {
	system := &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}}}
	req := &model.Request{Model: "m", MaxTokens: 200, Messages: []*model.Message{system, {Role: model.ConversationRoleUser, Parts: []model.Part{
		&model.TextPart{Text: "é"},
		&model.CitationsPart{Text: "cite", Citations: []model.Citation{{Title: "excluded metadata"}}},
		&model.DocumentPart{Name: "doc", Format: model.DocumentFormatTXT, Chunks: []string{"ab", "c"}, Context: "ctx"},
		&model.ToolUsePart{ID: "id", Name: "tool", Input: map[string]any{"x": 1}},
		&model.ToolResultPart{ToolUseID: "id", Content: map[string]any{"ok": true}},
		&model.ThinkingPart{Text: "think", Redacted: []byte{1, 2, 3}, Signature: "excluded signature"},
		&model.CacheCheckpointPart{},
	}, Meta: map[string]any{"excluded": "metadata"}}}, Tools: []*model.ToolDefinition{{Name: "tool", Description: "desc", InputSchema: map[string]any{"type": "object"}}}}
	facts, err := Measure(req, model.ContextLimits{ContextWindow: 1000}, []*model.Message{system})
	require.NoError(t, err)
	fixed := len("system") + len("tool") + len("desc") + len(`{"type":"object"}`)
	text := fixed + len("é") + len("cite") + len("doc") + len("abc") + len("ctx") + len("idtool") + len(`{"x":1}`) + len("id") + len(`{"ok":true}`) + len("think") + 3
	assert.Equal(t, RequestFacts{Model: "m", ContextWindow: 1000, OutputReserve: 200, ViewBearing: true, ViewTextBytes: 6, TextBytes: text, FixedBytes: fixed}, facts)
}

func TestFactsWithResult(t *testing.T) {
	key := runstore.CallKey{RunID: "run", Seq: 3, Attempt: 2}
	base := RequestFacts{Call: key, Model: "m", ContextWindow: 1000, OutputReserve: 200, TextBytes: 30, ViewBearing: true}
	overflow := model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "overflow", "", false, nil)
	for _, tc := range []struct {
		name    string
		usage   model.TokenUsage
		err     error
		input   int
		outcome RequestOutcome
	}{
		{"usage_includes_cache", model.TokenUsage{InputTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 30, OutputTokens: 100}, nil, 60, OutcomeOK},
		{"missing_usage", model.TokenUsage{}, nil, 0, OutcomeOK},
		{"overflow", model.TokenUsage{}, fmt.Errorf("wrapped: %w", overflow), 0, OutcomeOverflow},
		{"other_error", model.TokenUsage{}, errors.New("failure"), 0, OutcomeError},
		{"usage_overflow", model.TokenUsage{InputTokens: math.MaxInt, CacheReadTokens: 1}, nil, math.MaxInt, OutcomeOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := base.WithResult(tc.usage, tc.err)
			expected := base
			expected.InputTokens = tc.input
			expected.Outcome = tc.outcome
			assert.Equal(t, expected, facts)
			assert.Zero(t, base.InputTokens, "WithResult mutated pre-send facts")
			assert.Empty(t, base.Outcome, "WithResult mutated pre-send facts")
		})
	}
	assert.Equal(t, "pre_send_rejected", string(OutcomePreSendRejected))
}

func TestMeasureErrors(t *testing.T) {
	for _, req := range []*model.Request{
		nil,
		{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "id", Content: make(chan int)}}}}},
		{Messages: []*model.Message{{Parts: []model.Part{(*model.TextPart)(nil)}}}},
	} {
		_, err := Measure(req, model.ContextLimits{}, nil)
		assert.Error(t, err)
	}
}

func TestMeasureNullJSON(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "id", Content: nil}}}}}
	facts, err := Measure(req, model.ContextLimits{}, nil)
	require.NoError(t, err)
	assert.Equal(t, len("id")+len("null"), facts.TextBytes)
}
