package anthropic

import (
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func partsRequest(result model.ToolResultPart) *model.Request {
	result.ToolUseID = "call"
	return &model.Request{Messages: []*model.Message{
		{Role: "assistant", Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: "user", Parts: []model.Part{result}},
	}, Tools: []*model.ToolDefinition{{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object"}}}}
}
func partsHTTPClient(t *testing.T) (*Client, *[]byte, *atomic.Int32) {
	t.Helper()
	var body []byte
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		body = data
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		response := `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		if strings.Contains(r.URL.Path, "count-tokens") {
			response = `{"inputTokens":42}`
		}
		if _, err := io.WriteString(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	sdkClient := sdk.NewClient(option.WithAPIKey("test"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	client, err := New(&sdkClient.Messages, Options{DefaultModel: "claude-sonnet-4-5", MaxTokens: 128})
	require.NoError(t, err)
	return client, &body, calls
}
func TestToolResultPartsBody(t *testing.T) {
	image := model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}
	pdf := model.DocumentPart{Name: "manual.pdf", Format: "pdf", Bytes: []byte("%PDF-test")}
	for _, tc := range []struct {
		name   string
		result model.ToolResultPart
	}{
		{"json_image", model.ToolResultPart{Content: map[string]any{"ok": true}, Parts: []model.ToolResultContentPart{image}}},
		{"pdf", model.ToolResultPart{Content: "document", Parts: []model.ToolResultContentPart{pdf}}},
		{"empty_image", model.ToolResultPart{Parts: []model.ToolResultContentPart{&image}}},
		{"empty_text_image", model.ToolResultPart{Content: "", Parts: []model.ToolResultContentPart{image}}},
		{"ordered_image_pdf", model.ToolResultPart{Content: "attachments", Parts: []model.ToolResultContentPart{image, &pdf}}},
		{"is_error", model.ToolResultPart{Content: "failed", IsError: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, body, calls := partsHTTPClient(t)
			_, err := client.Complete(t.Context(), partsRequest(tc.result))
			require.NoError(t, err)
			assert.Equal(t, int32(1), calls.Load())
			assertPartsGolden(t, tc.name, *body)
			if tc.name == "pdf" {
				assert.Contains(t, string(*body), "JVBERi10ZXN0")
			} else if len(tc.result.Parts) > 0 {
				assert.Contains(t, string(*body), "AQID")
			}
		})
	}
}
func assertPartsGolden(t *testing.T, name string, body []byte) {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(body, &value))
	canonical, err := json.Marshal(value, json.Deterministic(true))
	require.NoError(t, err)
	path := filepath.Join("testdata", "tool_result_parts", name+".json")
	testutil.Assert(t, path, append(canonical, '\n'))
}
func TestToolResultPartsRejectBeforeHTTP(t *testing.T) {
	for _, part := range []model.ToolResultContentPart{
		model.ImagePart{Format: "bmp", Bytes: []byte{1}},
		model.DocumentPart{Name: "bad", Format: "exe", Bytes: []byte{1}},
		model.ImagePart{Format: "png", Bytes: make([]byte, 6_000_000)},
		model.DocumentPart{Name: "doc", Format: "pdf", URI: "https://example.com/a.pdf"},
		model.DocumentPart{Name: "doc", Format: "pdf", Bytes: make([]byte, 33_000_000)},
	} {
		client, _, calls := partsHTTPClient(t)
		_, err := client.Complete(t.Context(), partsRequest(model.ToolResultPart{Parts: []model.ToolResultContentPart{part}}))
		require.ErrorIs(t, err, model.ErrUnsupportedPart)
		var typed *model.UnsupportedPartError
		require.ErrorAs(t, err, &typed)
		assert.Zero(t, calls.Load())
	}
}

func TestUserDocumentBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  model.DocumentPart
	}{
		{"user_pdf", model.DocumentPart{Name: "manual", Format: "pdf", Bytes: []byte("%PDF-test"), Context: "reference", Cite: true}},
		{"user_text", model.DocumentPart{Name: "manual", Text: "plain document", Cite: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, body, _ := partsHTTPClient(t)
			_, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: "user", Parts: []model.Part{tc.doc}}}})
			require.NoError(t, err)
			assertPartsGolden(t, tc.name, *body)
		})
	}
}

func TestToolResultPartsCapabilities(t *testing.T) {
	client, _, _ := partsHTTPClient(t)
	support := client.ToolResultParts(t.Context(), &model.Request{Model: "any-model"})
	assert.Equal(t, 5_000_000, support.MaxImageBytes)
	assert.Equal(t, 24_000_000, support.MaxDocumentBytes)
	assert.Equal(t, []model.DocumentFormat{"pdf"}, support.DocumentFormats)
	assert.Empty(t, support.DocumentURISchemes)
}

func TestLiveToolResultParts(t *testing.T) {
	if testing.Short() || os.Getenv("LOOM_MCP_RUN_LIVE_MULTIMODAL") != "1" || os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("requires non-short mode, LOOM_MCP_RUN_LIVE_MULTIMODAL=1 and ANTHROPIC_API_KEY")
	}
	modelID := os.Getenv("ANTHROPIC_MODEL")
	if modelID == "" {
		t.Skip("ANTHROPIC_MODEL is required")
	}
	client, err := NewFromAPIKey(os.Getenv("ANTHROPIC_API_KEY"), modelID)
	require.NoError(t, err)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=")
	require.NoError(t, err)
	req := partsRequest(model.ToolResultPart{Content: "Describe the attached image briefly.", Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: png}}})
	req.MaxTokens = 128
	_, err = client.Complete(t.Context(), req)
	require.NoError(t, err)
}
