package callrecord

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsLegacyCallBytes(t *testing.T) {
	// Captured through the unmodified 87ad0e17 message codec, protocol 17.
	original, err := os.ReadFile("testdata/k22a_v17_request.json")
	require.NoError(t, err)
	req, err := DecodeRequest(original)
	require.NoError(t, err)
	result := req.Messages[1].Parts[0].(model.ToolResultPart)
	assert.Nil(t, result.Parts)
	for _, parts := range [][]model.ToolResultContentPart{nil, {}} {
		result.Parts = parts
		req.Messages[1].Parts[0] = result
		encoded, err := Encode(req)
		require.NoError(t, err)
		assert.Equal(t, string(original), string(encoded))
	}
}
func TestToolResultPartsRecordedMessage(t *testing.T) {
	original := &model.Message{Role: "user", Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: map[string]any{"ok": true}, Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte{1}}}}}}
	wire, err := Encode(original)
	require.NoError(t, err)
	got, err := DecodeMessage(wire)
	require.NoError(t, err)
	assert.Equal(t, original.Parts[0].(model.ToolResultPart).Parts, got.Parts[0].(model.ToolResultPart).Parts)
	assert.True(t, messageStable(wire))
}

func TestToolResultPartsLegacyStartedBytes(t *testing.T) {
	original, err := os.ReadFile("testdata/k22a_v17_started.json")
	require.NoError(t, err)
	started, err := DecodeStarted(original)
	require.NoError(t, err)
	requestBytes, err := os.ReadFile("testdata/k22a_v17_request.json")
	require.NoError(t, err)
	req, err := DecodeRequest(requestBytes)
	require.NoError(t, err)
	parts, _, err := BuildRequest(t.Context(), inmem.New(), "root", req, index.ViewRef{}, nil)
	require.NoError(t, err)
	assert.Equal(t, started.Request, parts, "recorded message hashes and request digest must stay byte-identical")
	started.Request = parts
	encoded, err := Encode(started)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(encoded))
}
