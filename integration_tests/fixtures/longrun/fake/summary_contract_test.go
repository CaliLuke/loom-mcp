package fake_test

import (
	"testing"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummariserSatisfiesRuntimeContract(t *testing.T) {
	for _, drop := range []bool{false, true} {
		client := &fake.Summariser{Faults: fake.Faults{DropFacts: drop}}
		req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "Keep output within 80 tokens. target = TAR-12345"}}}}}
		response, err := client.Complete(t.Context(), req)
		require.NoError(t, err)
		text, err := fake.RequestText(&model.Request{Messages: []*model.Message{&response.Content[0]}})
		require.NoError(t, err)
		_, err = summarize.Validate(summarize.ValidationInput{Text: text, DefaultPrompt: true, SummaryBytes: 320, ReplacedTokens: 5000, CurrentPos: 1, To: 2, RenderedCheckpoint: func(s string) (int, error) {
			return (len(s) + 3) / 4, nil
		}})
		require.NoError(t, err)
		if drop {
			assert.NotContains(t, text, "TAR-12345")
		} else {
			assert.Contains(t, text, "TAR-12345")
		}
	}
}
