package ollama_test

import (
	"errors"
	"net/http"
	"testing"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/require"
)

func TestClientPreservesCustomDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	sentinel := errors.New("custom default transport called")
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})
	client, err := ollamamodel.New(ollamamodel.Options{ServerURL: "http://unused.invalid", DefaultModel: "test"})
	require.NoError(t, err)
	_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
	}})
	require.ErrorIs(t, err, sentinel)
}
