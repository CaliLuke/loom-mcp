package openai_test

import (
	"context"
	"encoding/json/v2"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Recorded failure and code from OpenAI's own conformance fixture:
// https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs
const openAIOverflowBody = `{"error":{"code":"context_length_exceeded","type":"invalid_request_error","param":"input","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}`

func testContextOverflow(t *testing.T) {
	apiErr := openAIBodyError(t, openAIOverflowBody)
	response, err := openAIOverflowClient(t, &mockResponsesClient{err: apiErr}).Complete(context.Background(), openAIOverflowRequest())
	assert.Nil(t, response)
	pe := requireOpenAIOverflow(t, err)
	assert.Equal(t, 400, pe.HTTPStatus())
	assert.Equal(t, "req_overflow", pe.RequestID())
	require.ErrorIs(t, err, apiErr)

	t.Run("ordinary 400 remains SDK error", func(t *testing.T) {
		ordinary := openAIBodyError(t, `{"error":{"code":"invalid_value","type":"invalid_request_error","message":"Invalid value for temperature."}}`)
		response, err := openAIOverflowClient(t, &mockResponsesClient{err: ordinary}).Complete(context.Background(), openAIOverflowRequest())
		assert.Nil(t, response)
		require.ErrorIs(t, err, ordinary)
		_, classified := model.AsProviderError(err)
		assert.False(t, classified)
	})
}

func testStreamContextOverflow(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "setup"},
		{name: "receive"},
		{name: "failed event", body: `{"type":"response.failed","sequence_number":3,"response":{"id":"resp_1","object":"response","status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"usage":null}}`},
		{name: "error event", body: `{"type":"error","sequence_number":3,"code":"context_length_exceeded","message":"Your input exceeds the context window of this model.","param":"input"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire := newMockOpenAIStream(tc.body)
			switch tc.name {
			case "setup":
				wire = newMockOpenAIStreamError(openAIBodyError(t, openAIOverflowBody))
			case "receive":
				wire = newMockOpenAIStreamReadError(openAIBodyError(t, openAIOverflowBody))
			}
			stream, err := openAIOverflowClient(t, &mockResponsesClient{stream: wire}).Stream(context.Background(), openAIOverflowRequest())
			if tc.name == "setup" {
				assert.Nil(t, stream)
				assert.Equal(t, "responses stream", requireOpenAIOverflow(t, err).Operation())
				return
			}
			require.NoError(t, err)
			defer func() { assert.NoError(t, stream.Close()) }()
			chunk, err := stream.Recv()
			assert.Nil(t, chunk)
			assert.Equal(t, "responses stream", requireOpenAIOverflow(t, err).Operation())
			assert.Nil(t, stream.Response())
		})
	}
}

func openAIBodyError(t *testing.T, body string) *openai.Error {
	t.Helper()
	var wire struct {
		Error openai.Error `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire))
	wire.Error.StatusCode = 400
	// Reuse the existing SDK HTTP fixture to preserve request metadata.
	base := newOpenAIRateLimitError(t)
	wire.Error.Request = base.Request
	wire.Error.Response = base.Response
	wire.Error.Response.StatusCode = 400
	wire.Error.Response.Header = map[string][]string{"X-Request-Id": {"req_overflow"}}
	return &wire.Error
}

func openAIOverflowClient(t *testing.T, stub *mockResponsesClient) *openaimodel.Client {
	t.Helper()
	client, err := openaimodel.New(openaimodel.Options{Client: stub, DefaultModel: "gpt-4o"})
	require.NoError(t, err)
	return client
}

func openAIOverflowRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
}

func requireOpenAIOverflow(t *testing.T, err error) *model.ProviderError {
	t.Helper()
	var pe *model.ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, model.ProviderErrorKindContextOverflow, pe.Kind())
	assert.Equal(t, "openai", pe.Provider())
	assert.Equal(t, "context_length_exceeded", pe.Code())
	assert.False(t, pe.Retryable())
	return pe
}
