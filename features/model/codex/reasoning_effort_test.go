package codex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "low", "xhigh", "ultra", "future_level"} {
		t.Run(effort, func(t *testing.T) {
			for _, lite := range []bool{false, true} {
				client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", ReasoningEffort: effort, ResponsesLite: lite})
				require.NoError(t, err)
				request := testRequest()
				request.Thinking = &model.ThinkingOptions{Enable: true}
				built, err := client.buildRequest(request)
				require.NoError(t, err)
				assert.Equal(t, "auto", built.body.Reasoning["summary"])
				if effort == "" {
					assert.NotContains(t, built.body.Reasoning, "effort")
				} else {
					assert.Equal(t, effort, built.body.Reasoning["effort"])
				}
				if lite {
					assert.Equal(t, "all_turns", built.body.Reasoning["context"])
				}
				for _, websocket := range []bool{false, true} {
					body, err := built.marshal(websocket)
					require.NoError(t, err)
					if effort != "" {
						assert.Contains(t, string(body), `"effort":"`+effort+`"`)
					}
				}
			}
		})
	}
}

func TestReasoningEffortDoesNotRequireSummary(t *testing.T) {
	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", ReasoningEffort: "high"})
	require.NoError(t, err)
	built, err := client.buildRequest(testRequest())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"effort": "high"}, built.body.Reasoning)
}

func TestReasoningEffortRejectsMalformedValues(t *testing.T) {
	for _, effort := range []string{" high", "high ", "HIGH", "bad\nvalue", strings.Repeat("a", 33)} {
		client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", ReasoningEffort: effort})
		assert.Nil(t, client)
		assert.ErrorContains(t, err, "reasoning effort")
	}
}
