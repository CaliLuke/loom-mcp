package gateway

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsRoundTrip(t *testing.T) {
	provider := &captureProvider{}
	server, err := NewServer(WithProvider(provider))
	require.NoError(t, err)
	client := NewRemoteClient(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		var decoded model.Request
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, err
		}
		return server.Complete(ctx, &decoded)
	}, provider.Stream)
	result := model.ToolResultPart{ToolUseID: "call", Content: "ok", Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte{1, 2}}, model.DocumentPart{Name: "pdf", Format: "pdf", Bytes: []byte{3, 4}}}}
	_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: "assistant", Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}}, {Role: "user", Parts: []model.Part{result}}}})
	require.NoError(t, err)
	assert.Equal(t, result, provider.lastReq.Load().(model.Request).Messages[1].Parts[0])
}
