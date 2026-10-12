package continuation

import (
	"encoding/json/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCarryPreviewsOriginalRenderedParts(t *testing.T) {
	original := strings.Repeat("abcdef", 30000)
	arguments := map[string]string{"q": original}
	encoded, err := json.Marshal(arguments)
	require.NoError(t, err)
	for _, capBytes := range []int{2048, 8192, 30000} {
		t.Run(strconv.Itoa(capBytes), func(t *testing.T) {
			entries := []Entry{{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant}, Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: original}, model.ToolUsePart{ID: "call", Name: "tool", Input: arguments}, model.ToolResultPart{ToolUseID: "call", Content: strings.Repeat("result", 30000)}}}}}}
			for run := 2; run <= 3; run++ {
				plan, err := Carry(entries, index.ViewRef{}, capBytes/(run-1), 256)
				require.NoError(t, err)
				require.Len(t, plan.Tail, 1)
				parts := plan.Tail[0].Messages[0].Parts
				assertPreviewOmission(t, parts[0].(model.TextPart).Text, len(original))
				data, err := json.Marshal(parts[1].(model.ToolUsePart).Input)
				require.NoError(t, err)
				var input map[string]string
				require.NoError(t, json.Unmarshal(data, &input))
				require.Len(t, input, 1)
				preview, ok := input[Preview]
				require.True(t, ok)
				assert.NotContains(t, preview, `"preview"`)
				assert.NotContains(t, preview, `\"`)
				assertPreviewOmission(t, preview, len(encoded))
				entries = plan.Tail
			}
		})
	}
}

func assertPreviewOmission(t *testing.T, text string, originalBytes int) {
	t.Helper()
	matches := regexp.MustCompile(`… \[([0-9]+) bytes not shown\] …`).FindAllStringSubmatch(text, -1)
	require.Len(t, matches, 1)
	omitted, err := strconv.Atoi(matches[0][1])
	require.NoError(t, err)
	assert.Equal(t, originalBytes, len(text)-len(matches[0][0])+omitted, "omission count: %s", matches[0][0])
}
