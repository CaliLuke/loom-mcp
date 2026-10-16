package codex

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestCompleteAcceptsResponseMetadata(t *testing.T) {
	client := newEventsClient(t, responseMetadataEvents()...)
	response, err := client.Complete(t.Context(), testRequest())
	require.NoError(t, err)
	assert.Equal(t, []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "hello"}}}}, response.Content)
}

func TestStreamAcceptsResponseMetadata(t *testing.T) {
	client := newEventsClient(t, responseMetadataEvents()...)
	stream, err := client.Stream(t.Context(), testRequest())
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, stream.Close())
	})
	first, err := stream.Recv()
	require.NoError(t, err)
	second, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, textChunk("hel"), first)
	assert.Equal(t, textChunk("lo"), second)
	assert.Nil(t, stream.Response())
	usage, err := stream.Recv()
	require.NoError(t, err)
	assert.IsType(t, model.UsageChunk{}, usage)
	stop, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, model.StopChunk{Reason: "completed"}, stop)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NotNil(t, stream.Response())
	assert.Equal(t, []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "hello"}}}}, stream.Response().Content)
}

// responseMetadataEvents interleaves the provider's non-content metadata with text.
func responseMetadataEvents() []string {
	return []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
		`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"hel"}`,
		`{"type":"response.metadata","response_id":"resp","sequence_number":3,"metadata":{}}`,
		`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"lo"}`,
		`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"hello"}`,
		`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"msg","type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`,
	}
}
