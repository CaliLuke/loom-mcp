package gemini_test

import (
	"encoding/json/v2"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestHostedCodeEvaluationCurrentBoundary(t *testing.T) {
	fixtures := os.DirFS("testdata/hosted_code")
	paths, err := fs.Glob(fixtures, "*.response.json")
	require.NoError(t, err)
	require.Len(t, paths, 9)
	for _, path := range paths {
		t.Run(strings.TrimSuffix(path, ".response.json"), func(t *testing.T) {
			raw, readErr := fs.ReadFile(fixtures, path)
			require.NoError(t, readErr)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() {
					assert.NoError(t, r.Body.Close())
				}()
				var request map[string]any
				if !assert.NoError(t, json.UnmarshalRead(r.Body, &request)) {
					return
				}
				assert.NotContains(t, request, "tools", "this evaluates unsupported responses, not enablement")
				w.Header().Set("Content-Type", "application/json")
				_, writeErr := w.Write(raw)
				assert.NoError(t, writeErr)
			}))
			t.Cleanup(server.Close)
			sdk, clientErr := genai.NewClient(t.Context(), &genai.ClientConfig{Backend: genai.BackendGeminiAPI, APIKey: "test", HTTPClient: server.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}}})
			require.NoError(t, clientErr)
			decoded, decodeErr := sdk.Models.GenerateContent(t.Context(), "test", []*genai.Content{genai.NewContentFromText("question", genai.RoleUser)}, nil)
			require.NoError(t, decodeErr)
			require.Len(t, decoded.Candidates, 1)
			var fixture genai.GenerateContentResponse
			require.NoError(t, json.Unmarshal(raw, &fixture))
			assert.Equal(t, fixture.Candidates[0].Content.Parts, decoded.Candidates[0].Content.Parts, "real SDK preserves code/result bytes and sequence")
			provider, providerErr := geminimodel.New(geminimodel.Options{Client: sdk.Models, DefaultModel: "test"})
			require.NoError(t, providerErr)
			client, validatedErr := model.NewClient(provider)
			require.NoError(t, validatedErr)
			response, completeErr := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "question"}}}}})
			var text []model.Part
			var kinds []string
			for _, part := range fixture.Candidates[0].Content.Parts {
				switch {
				case part.Text != "":
					text = append(text, model.TextPart{Text: part.Text})
					kinds = append(kinds, "text")
				case part.ExecutableCode != nil:
					kinds = append(kinds, "executable_code")
				case part.CodeExecutionResult != nil:
					kinds = append(kinds, "code_execution_result")
				}
			}
			if len(text) == 0 {
				assert.Nil(t, response)
				var rejection *model.OutputValidationError
				require.ErrorAs(t, completeErr, &rejection)
				assert.Equal(t, model.OutputValidationResponseShape, rejection.Kind())
				require.NotNil(t, rejection.Usage())
				assert.Equal(t, 13, rejection.Usage().TotalTokens)
				t.Logf("SDK parts=%v; current=output_shape rejection; usage=13", kinds)
			} else {
				require.NoError(t, completeErr)
				require.Len(t, response.Content, 1)
				assert.Equal(t, text, response.Content[0].Parts)
				assert.Empty(t, response.ToolCalls)
				assert.Equal(t, 13, response.Usage.TotalTokens)
				t.Logf("SDK parts=%v; current=text-only %d parts; usage=13", kinds, len(text))
			}
		})
	}
}
