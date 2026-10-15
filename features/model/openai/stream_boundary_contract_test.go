package openai_test

import (
	"io"
	"testing"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamResolvesToolIdentityAfterArgumentDeltas(t *testing.T) {
	mock := &mockResponsesClient{stream: newMockOpenAIStream(
		`{"type":"response.function_call_arguments.delta","item_id":"item","delta":""}`,
		`{"type":"response.function_call_arguments.delta","item_id":"item","delta":"{\"query\":"}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item","name":"lookup","call_id":"call","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"item","delta":"\"docs\"}"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"item","name":"lookup","call_id":"call","arguments":"{\"query\":\"docs\"}"}]}}`,
	)}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)
	stream, err := client.Stream(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "lookup"}}}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, stream.Close())
	})
	chunks := testutil.CollectStreamChunks(t, stream)
	require.Len(t, chunks, 5)
	assert.Equal(t, model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{Name: "lookup", ID: "call", Delta: `{"query":`}}, chunks[0])
	assert.Equal(t, model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{Name: "lookup", ID: "call", Delta: `"docs"}`}}, chunks[1])
	require.NotNil(t, stream.Response())
	require.Len(t, stream.Response().ToolCalls, 1)
	assert.JSONEq(t, `{"query":"docs"}`, string(stream.Response().ToolCalls[0].Payload))
}

func TestOpenAIStreamFailsWithoutAcceptedResponseOnProviderError(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.failed","response":{"error":{"code":"server_error","message":"provider failed"}}}`,
		`{"type":"error","code":"server_error","message":"provider failed"}`,
	} {
		mock := &mockResponsesClient{stream: newMockOpenAIStream(event)}
		client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
		require.NoError(t, err)
		stream, err := client.Stream(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
		require.NoError(t, err)
		t.Cleanup(func() {
			assert.NoError(t, stream.Close())
		})
		chunk, err := stream.Recv()
		require.ErrorContains(t, err, "provider failed")
		require.NotErrorIs(t, err, io.EOF)
		assert.Nil(t, chunk)
		assert.Nil(t, stream.Response())
	}
}
