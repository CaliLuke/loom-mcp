package ollama_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countedResponseBody struct {
	io.Reader
	read   int
	closed bool
}

func (body *countedResponseBody) Read(data []byte) (int, error) {
	n, err := body.Reader.Read(data)
	body.read += n
	return n, err
}

func (body *countedResponseBody) Close() error {
	body.closed = true
	return nil
}

func TestClientCompleteBoundsResponseBody(t *testing.T) {
	const limit = 16 << 20
	const response = `{"message":{"role":"assistant","content":"ok"},"done":true}`
	for _, tt := range []struct {
		name    string
		size    int
		readErr error
	}{
		{name: "below", size: len(response)},
		{name: "at limit", size: limit},
		{name: "above limit", size: limit + 4096},
		{name: "read failure", size: len(response), readErr: io.ErrUnexpectedEOF},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &countedResponseBody{Reader: io.MultiReader(strings.NewReader(response), strings.NewReader(strings.Repeat(" ", tt.size-len(response))))}
			if tt.readErr != nil {
				body.Reader = iotest.ErrReader(tt.readErr)
			}
			client, err := ollamamodel.New(ollamamodel.Options{
				ServerURL: "http://unused.invalid", DefaultModel: "test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: -1}, nil
				})},
			})
			require.NoError(t, err)
			result, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
			}})
			switch {
			case tt.readErr != nil:
				require.ErrorIs(t, err, tt.readErr)
				assert.Nil(t, result)
			case tt.size > limit:
				require.ErrorContains(t, err, "response exceeds")
				assert.Nil(t, result)
				assert.LessOrEqual(t, body.read, limit+1)
			default:
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
			assert.True(t, body.closed)
		})
	}
}
