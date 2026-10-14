package codex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalResponseKeepsProviderMessageBoundaries(t *testing.T) {
	raw := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, sseEvents(
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"first","type":"message"}}`,
			`{"type":"response.output_text.delta","item_id":"first","output_index":0,"content_index":0,"delta":"hel"}`,
			`{"type":"response.output_text.delta","item_id":"first","output_index":0,"content_index":0,"delta":"lo"}`,
			`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"first","type":"message","content":[{"type":"output_text","text":"hello"}]},{"id":"second","type":"message","content":[{"type":"output_text","text":"world"}]}]}}`,
		)), nil
	}))
	client, err := model.NewClient(raw)
	require.NoError(t, err)
	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, response.Content, 2)
	assert.Equal(t, []model.Part{model.TextPart{Text: "hello"}}, response.Content[0].Parts)
	assert.Equal(t, []model.Part{model.TextPart{Text: "world"}}, response.Content[1].Parts)
	stream, err := client.Stream(context.Background(), testRequest())
	require.NoError(t, err)
	var deltas []string
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		require.NoError(t, recvErr)
		if text, ok := chunk.(model.TextChunk); ok {
			deltas = append(deltas, text.Message.Parts[0].(model.TextPart).Text)
		}
	}
	require.NoError(t, stream.Finalize(nil))
	require.NotNil(t, stream.Response())
	assert.Equal(t, response.Content, stream.Response().Content)
	assert.Equal(t, []string{"hel", "lo", "world"}, deltas, "live presentation remains incremental")
}
