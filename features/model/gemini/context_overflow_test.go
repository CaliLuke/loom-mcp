package gemini_test

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func testContextOverflow(t *testing.T) {
	// Captured bodies: https://github.com/googleapis/python-genai/issues/2050
	// and https://github.com/google-gemini/gemini-cli/issues/22609 (no input count).
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "token counts", body: `{"error":{"code":400,"message":"The input token count (25270) exceeds the maximum number of tokens allowed (4000).","status":"INVALID_ARGUMENT"}}`, want: true},
		{name: "no input count", body: `{"error":{"code":400,"message":"The input token count exceeds the maximum number of tokens allowed 1048576.","status":"INVALID_ARGUMENT"}}`, want: true},
		{name: "ordinary 400", body: `{"error":{"code":400,"message":"Invalid value at generation_config.temperature","status":"INVALID_ARGUMENT"}}`},
		{name: "wrong HTTP status", body: `{"error":{"code":500,"message":"The input token count exceeds the maximum number of tokens allowed 1048576.","status":"INVALID_ARGUMENT"}}`},
		{name: "wrong status", body: `{"error":{"code":400,"message":"The input token count exceeds the maximum number of tokens allowed 1048576.","status":"FAILED_PRECONDITION"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var wire struct {
				Error genai.APIError `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(tc.body), &wire))
			client, err := geminimodel.New(geminimodel.Options{Client: &mockModelsClient{generateErr: wire.Error}, DefaultModel: "gemini-2.5-flash"})
			require.NoError(t, err)
			response, err := client.Complete(context.Background(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
			assert.Nil(t, response)
			if !tc.want {
				var apiErr genai.APIError
				require.ErrorAs(t, err, &apiErr)
				_, classified := model.AsProviderError(err)
				assert.False(t, classified, "non-overflow handling must stay unchanged")
				return
			}
			var pe *model.ProviderError
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, model.ProviderErrorKindContextOverflow, pe.Kind())
			assert.False(t, pe.Retryable())
			assert.Equal(t, "gemini", pe.Provider())
			assert.Equal(t, 400, pe.HTTPStatus())
			assert.Equal(t, "INVALID_ARGUMENT", pe.Code())
		})
	}
}
