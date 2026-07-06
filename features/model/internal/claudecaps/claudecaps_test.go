package claudecaps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemperatureSupported(t *testing.T) {
	cases := []struct {
		name    string
		modelID string
		want    bool
	}{
		{"opus-4-6 supports sampling", "claude-opus-4-6", true},
		{"opus-4-6 dated snapshot supports sampling", "claude-opus-4-6-20260201", true},
		{"opus-4-6 bedrock geo supports sampling", "global.anthropic.claude-opus-4-6-v1", true},
		{"opus-4-7 omits sampling", "claude-opus-4-7", false},
		{"opus-4-7 dated snapshot omits sampling", "claude-opus-4-7-20260315", false},
		{"opus-4-7 vertex dated omits sampling", "claude-opus-4-7@20260315", false},
		{"opus-4-7 bedrock geo omits sampling", "us.anthropic.claude-opus-4-7", false},
		{"future opus-4-10 omits sampling", "claude-opus-4-10", false},
		{"future opus-5 omits sampling", "claude-opus-5", false},
		{"opus-4-0 dated supports sampling", "claude-opus-4-20250514", true},
		{"legacy claude-3-opus supports sampling", "claude-3-opus-20240229", true},
		{"sonnet-4-5 supports sampling", "claude-sonnet-4-5-20250929", true},
		{"sonnet-5 omits sampling", "claude-sonnet-5", false},
		{"sonnet-5 bedrock geo omits sampling", "us.anthropic.claude-sonnet-5", false},
		{"legacy 3.5 sonnet supports sampling", "claude-3-5-sonnet-20241022", true},
		{"haiku-4-5 supports sampling", "claude-haiku-4-5-20251001", true},
		{"future haiku-5 omits sampling", "claude-haiku-5", false},
		{"fable-5 omits sampling", "claude-fable-5", false},
		{"fable-5 bedrock suffixed omits sampling", "global.anthropic.claude-fable-5-v1:0", false},
		{"mythos-5 omits sampling", "us.anthropic.claude-mythos-5", false},
		{"mythos-preview supports sampling", "claude-mythos-preview", true},
		{"non-claude model supports sampling", "amazon.nova-pro-v1:0", true},
		{"empty model id supports sampling", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TemperatureSupported(tc.modelID))
		})
	}
}

func TestAdaptiveThinkingRequired(t *testing.T) {
	cases := []struct {
		name    string
		modelID string
		want    bool
	}{
		{"opus-4-6 in-region", "anthropic.claude-opus-4-6-v1", true},
		{"opus-4-6 us geo", "us.anthropic.claude-opus-4-6-v1", true},
		{"opus-4-6 global", "global.anthropic.claude-opus-4-6-v1", true},
		{"opus-4-7 in-region", "anthropic.claude-opus-4-7", true},
		{"opus-4-8", "us.anthropic.claude-opus-4-8", true},
		{"future opus-5", "claude-opus-5", true},
		{"fable-5 in-region", "anthropic.claude-fable-5", true},
		{"fable-5 global", "global.anthropic.claude-fable-5", true},
		{"fable-5 suffixed", "us.anthropic.claude-fable-5-v1:0", true},
		{"mythos-5", "us.anthropic.claude-mythos-5", true},
		{"opus-4-5 legacy config", "anthropic.claude-opus-4-5-20251101-v1", false},
		{"opus-4-0 dated", "claude-opus-4-20250514", false},
		{"sonnet-4-5", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", false},
		{"haiku-4-5", "global.anthropic.claude-haiku-4-5-20251001-v1:0", false},
		{"mythos-preview", "claude-mythos-preview", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, AdaptiveThinkingRequired(tc.modelID))
		})
	}
}

func TestIsFableGeneration(t *testing.T) {
	cases := []struct {
		name    string
		modelID string
		want    bool
	}{
		{"fable-5 bare", "claude-fable-5", true},
		{"fable-5 bedrock suffixed", "global.anthropic.claude-fable-5-v1:0", true},
		{"mythos-5", "claude-mythos-5", true},
		{"future fable-6", "claude-fable-6", true},
		{"mythos-preview is not claude 5", "claude-mythos-preview", false},
		{"opus", "claude-opus-4-8", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsFableGeneration(tc.modelID))
		})
	}
}
