package ollama_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestClientEncodesThinkingOption(t *testing.T) {
	for _, mode := range []string{"complete", "stream"} {
		t.Run(mode, func(t *testing.T) {
			for _, tt := range []struct {
				name     string
				thinking *model.ThinkingOptions
				want     any
			}{
				{name: "omitted"},
				{name: "enabled", thinking: &model.ThinkingOptions{Enable: true}, want: true},
				{name: "disabled", thinking: &model.ThinkingOptions{Enable: false}, want: false},
				{name: "low", thinking: &model.ThinkingOptions{Effort: "low"}, want: "low"},
				{name: "medium", thinking: &model.ThinkingOptions{Effort: "medium"}, want: "medium"},
				{name: "high", thinking: &model.ThinkingOptions{Effort: "high"}, want: "high"},
				{name: "effort overrides enabled", thinking: &model.ThinkingOptions{Enable: true, Effort: "low"}, want: "low"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					captured := make(chan map[string]any, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						assert.NoError(t, json.UnmarshalRead(r.Body, &body))
						captured <- body
						assert.NoError(t, json.MarshalWrite(w, map[string]any{
							"model": "gpt-oss:20b", "done": true, "done_reason": "stop",
							"message": map[string]any{"role": "assistant", "content": "ok"},
						}))
					}))
					defer server.Close()
					client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "gpt-oss:20b"})
					require.NoError(t, err)

					require.NoError(t, callThinkingRequest(t, client, mode, tt.thinking))

					body := <-captured
					assert.Equal(t, mode == "stream", body["stream"])
					if tt.want == nil {
						assert.NotContains(t, body, "think")
					} else {
						assert.Equal(t, tt.want, body["think"])
					}
				})
			}
		})
	}
}

func TestClientRejectsInvalidThinkingEffortBeforeHTTP(t *testing.T) {
	for _, mode := range []string{"complete", "stream"} {
		t.Run(mode, func(t *testing.T) {
			for _, effort := range []string{"unsupported", "HIGH", " low "} {
				t.Run(effort, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Error("invalid effort must not reach HTTP")
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer server.Close()
					client, err := ollamamodel.New(ollamamodel.Options{ServerURL: server.URL, DefaultModel: "gpt-oss:20b"})
					require.NoError(t, err)

					err = callThinkingRequest(t, client, mode, &model.ThinkingOptions{Effort: effort})
					assert.ErrorContains(t, err, "ollama: unsupported thinking effort")
				})
			}
		})
	}
}

// callThinkingRequest exercises both public request paths and drains streams.
func callThinkingRequest(t *testing.T, client *ollamamodel.Client, mode string, thinking *model.ThinkingOptions) error {
	t.Helper()
	req := &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}}},
		Thinking: thinking,
	}
	if mode == "complete" {
		_, err := client.Complete(context.Background(), req)
		return err
	}
	stream, err := client.Stream(context.Background(), req)
	if err != nil {
		return err
	}
	defer func() {
		assert.NoError(t, stream.Close())
	}()
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
