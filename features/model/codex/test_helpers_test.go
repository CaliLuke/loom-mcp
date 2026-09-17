package codex

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type testReadCloser struct {
	io.Reader
	closeErr error
}

func (r *testReadCloser) Close() error {
	return r.closeErr
}

func testRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{
		Role: model.ConversationRoleUser,
		Parts: []model.Part{
			model.TextPart{Text: "hello"},
		},
	}}}
}

func testCredentials(context.Context) (Credentials, error) {
	return Credentials{AccessToken: "secret-token", AccountID: "account-1", Residency: "us"}, nil
}

func newSSETestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		HTTPClient:       &http.Client{Transport: transport},
		Transport:        TransportSSE,
		DefaultModel:     "gpt-codex",
	})
	require.NoError(t, err)
	return client
}

func sseResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sseEvents(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	return body.String()
}

func emptyTerminalEvent() string {
	return `{"type":"response.completed","response":{"id":"resp-1","status":"completed","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`
}

func textTerminalEvent(text string) string {
	return `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"msg-1","type":"message","content":[{"type":"output_text","text":"` + text + `"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`
}
