package bedrock

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func FuzzToolFragmentPayload(f *testing.F) {
	f.Add(`{"query":`, `"loom"}`)
	f.Add("", "")
	f.Add("{", "")
	f.Add(`{"nested":[1,`, `2]}`)

	f.Fuzz(func(t *testing.T, left, right string) {
		if len(left)+len(right) > 1<<20 {
			return
		}
		buffer := toolBuffer{fragments: []string{left, right}}
		payload, err := decodeToolPayload(buffer.finalInput())
		trimmed := strings.TrimSpace(left + right)
		if trimmed == "" {
			trimmed = "{}"
		}
		if !jsontext.Value([]byte(trimmed)).IsValid() {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, trimmed, string(payload))
	})
}

func TestChunkProcessorRejectsMalformedToolFragments(t *testing.T) {
	processor := newChunkProcessor(
		func(model.Chunk) error { return nil },
		nil,
		nil,
		nil,
		"",
		model.ModelClassDefault,
		nil,
	)
	processor.toolBlocks[1] = &toolBuffer{
		name:      "lookup",
		id:        "call-1",
		fragments: []string{"{"},
	}

	err := processor.emitFinalToolCall(1)
	require.EqualError(t, err, `bedrock stream: tool call "call-1" payload: invalid JSON`)
	require.NotContains(t, processor.toolBlocks, 1)
}
