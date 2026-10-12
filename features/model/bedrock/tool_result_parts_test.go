package bedrock

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
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
		response := `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"metrics":{"latencyMs":1}}`
		if strings.Contains(r.URL.Path, "count-tokens") {
			response = `{"inputTokens":42}`
		}
		if _, err := io.WriteString(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	sdkClient := bedrockruntime.New(bedrockruntime.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	})})
	client, err := New(sdkClient, Options{Runtime: sdkClient, DefaultModel: "us.anthropic.claude-sonnet-4-5", MaxTokens: 128})
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
			if tc.name == "empty_image" || tc.name == "empty_text_image" {
				var wire struct {
					Messages []struct {
						Content []map[string]struct {
							Content []map[string]any `json:"content"`
						} `json:"content"`
					} `json:"messages"`
				}
				require.NoError(t, json.Unmarshal(*body, &wire))
				require.Len(t, wire.Messages, 2)
				require.Len(t, wire.Messages[1].Content, 1)
				attachments := wire.Messages[1].Content[0]["toolResult"].Content
				require.Len(t, attachments, 1)
				assert.Contains(t, attachments[0], "image")
			}
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

func TestToolResultPartsCountTokens(t *testing.T) {
	client, body, _ := partsHTTPClient(t)
	count, err := client.CountTokens(t.Context(), partsRequest(model.ToolResultPart{Content: "image", Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}}}))
	require.NoError(t, err)
	assert.Equal(t, 42, count.InputTokens)
	assert.Contains(t, string(*body), "AQID")
}
func TestToolResultPartsNonClaude(t *testing.T) {
	client, _, calls := partsHTTPClient(t)
	req := partsRequest(model.ToolResultPart{Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte{1}}}})
	req.Model = "amazon.nova-pro-v1:0"
	_, err := client.Complete(t.Context(), req)
	var typed *model.UnsupportedPartError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "model", typed.Reason)
	assert.Zero(t, calls.Load())
}
func TestEncodePartRejectsUnknown(t *testing.T) {
	_, err := newMessageEncodeState(t.Context(), nil, nil).encodePart(model.ConversationRoleUser, nil)
	assert.Error(t, err)
}

func TestToolResultPartsCapabilities(t *testing.T) {
	client, _, _ := partsHTTPClient(t)
	for _, id := range []string{"anthropic.claude-3-haiku-20240307-v1:0", "eu.anthropic.claude-sonnet-4-5", "us.anthropic.claude-opus-4-6", "global.anthropic.claude-opus-4-7"} {
		support := client.ToolResultParts(t.Context(), &model.Request{Model: id})
		assert.Equal(t, 3_750_000, support.MaxImageBytes)
		assert.Equal(t, 4_500_000, support.MaxDocumentBytes)
		assert.Len(t, support.DocumentFormats, 9)
		assert.Equal(t, []string{"s3"}, support.DocumentURISchemes)
	}
	assert.Equal(t, model.ToolResultPartSupport{}, client.ToolResultParts(t.Context(), &model.Request{Model: "amazon.nova-pro-v1:0"}))
}

func TestSystemPartsRejectBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		kind string
		part model.Part
	}{
		{"image", model.ImagePart{Format: "png", Bytes: []byte{1}}},
		{"document", model.DocumentPart{Name: "manual", Format: "pdf", Bytes: []byte("%PDF-test")}},
		{"tool_use", model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}},
		{"tool_result", model.ToolResultPart{ToolUseID: "call", Content: "result"}},
		{"thinking", model.ThinkingPart{Text: "thought", Signature: "signed"}},
		{"citations", model.CitationsPart{Text: "cited"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			client, _, calls := partsHTTPClient(t)
			req := &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}, tc.part}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
			}, Tools: []*model.ToolDefinition{{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object"}}}}
			_, err := client.Complete(t.Context(), req)
			require.ErrorIs(t, err, model.ErrUnsupportedPart)
			var typed *model.UnsupportedPartError
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, "bedrock", typed.Provider)
			assert.Equal(t, "system", typed.Placement)
			assert.Equal(t, tc.kind, typed.Kind)
			assert.Zero(t, calls.Load())
		})
	}
}
