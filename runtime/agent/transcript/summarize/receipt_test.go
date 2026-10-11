package summarize

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReceiptAnnotations(t *testing.T) {
	lines := []index.ReceiptLine{{Kind: "call", ToolUseID: "call", From: 2, Through: 2}}
	plan := Plan{Unsummarised: []ByteRange{{MessageID: 3, Part: 1, ToolUseID: "call", Start: 17, End: 29, IsResult: true}}, Annotations: []PartAnnotation{{MessageID: 1, Part: 2, Kind: "image", Format: "png", Bytes: 123}}}
	got, err := AnnotateReceipts(lines, plan)
	require.NoError(t, err)
	assert.Empty(t, lines[0].Unsummarised)
	assert.Equal(t, []index.ByteRange{{From: 17, Through: 28}}, got[0].Unsummarised)
	require.Len(t, got, 2)
	assert.Equal(t, "part", got[1].Kind)
	assert.Equal(t, index.MessageID(1), got[1].MessageID)
	assert.Equal(t, 2, got[1].Part)
	assert.Equal(t, "not summarised: image", got[1].Text)
	assert.Equal(t, 123, got[1].Bytes)
}
