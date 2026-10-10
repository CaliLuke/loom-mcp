package ollama_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Ollama chat truncates to num_ctx; there is no documented chat overflow error
// to classify. See https://github.com/ollama/ollama/blob/main/server/prompt.go.
// This ordinary chat error must not acquire a context_overflow classification.
const ollamaOrdinaryErrorBody = `{"error":"model 'missing' not found"}`

func testContextOverflow(t *testing.T) {
	client := ollamaNoOverflowClient(t)
	response, err := client.Complete(context.Background(), ollamaOverflowRequest())
	assert.Nil(t, response)
	require.ErrorContains(t, err, "not found")
	_, classified := model.AsProviderError(err)
	assert.False(t, classified)
}

func testStreamContextOverflow(t *testing.T) {
	stream, err := ollamaNoOverflowClient(t).Stream(context.Background(), ollamaOverflowRequest())
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()
	chunk, err := stream.Recv()
	assert.Nil(t, chunk)
	require.ErrorContains(t, err, "not found")
	_, classified := model.AsProviderError(err)
	assert.False(t, classified)
	assert.Nil(t, stream.Response())
}

func ollamaNoOverflowClient(t *testing.T) *ollamamodel.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, ollamaOrdinaryErrorBody+"\n")
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "llama3.1"})
	require.NoError(t, err)
	return client
}

func ollamaOverflowRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
}
