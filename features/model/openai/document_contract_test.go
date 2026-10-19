package openai_test

import (
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type documentTransport struct {
	request []byte
	calls   int
	stream  bool
}

// Minimal one-page PDF, with an odd byte count to exercise context rounding.
// Escaped xref padding preserves the required fixed-width entries.
var documentPDF = []byte(
	"%PDF-1.4\n" +
		"1 0 obj\n" +
		"<< /Type /Catalog /Pages 2 0 R >>\n" +
		"endobj\n" +
		"2 0 obj\n" +
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>\n" +
		"endobj\n" +
		"3 0 obj\n" +
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] >>\n" +
		"endobj\n" +
		"xref\n" +
		"0 4\n" +
		"0000000000 65535 f \n" +
		"0000000009 00000 n \n" +
		"0000000058 00000 n \n" +
		"0000000115 00000 n \n" +
		"trailer\n" +
		"<< /Size 4 /Root 1 0 R >>\n" +
		"startxref\n" +
		"184\n" +
		"%%EOF\n",
)

func TestOpenAIDocumentWirePayload(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[streaming], func(t *testing.T) {
			transport := &documentTransport{stream: streaming}
			provider := newDocumentProvider(t, transport)
			req := documentRequest(model.DocumentPart{Name: " Résumé.pdf ", Format: model.DocumentFormatPDF, Bytes: documentPDF})
			req.Messages[0].Parts = append([]model.Part{model.TextPart{Text: "before"}}, req.Messages[0].Parts...)
			req.Messages[0].Parts = append(req.Messages[0].Parts,
				model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")},
				model.TextPart{Text: "after"},
				model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: documentPDF},
			)
			if streaming {
				stream, err := provider.Stream(t.Context(), req)
				require.NoError(t, err)
				defer func() { assert.NoError(t, stream.Close()) }()
				for {
					_, err := stream.Recv()
					if err != nil {
						require.Equal(t, io.EOF, err)
						break
					}
				}
			} else {
				_, err := provider.Complete(t.Context(), req)
				require.NoError(t, err)
			}
			assert.Equal(t, 1, transport.calls)
			want := map[string]any{
				"model": "gpt-4o",
				"input": []any{map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "input_text", "text": "before"},
						map[string]any{"type": "input_file", "filename": " Résumé.pdf ", "file_data": "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(documentPDF)},
						map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,cG5n"},
						map[string]any{"type": "input_text", "text": "after"},
						map[string]any{"type": "input_file", "filename": "spec", "file_data": "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(documentPDF)},
					},
				}},
			}
			if streaming {
				want["stream"] = true
			}
			expected, err := json.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(transport.request))
		})
	}
}

func TestOpenAIDocumentSnapshotAndAccounting(t *testing.T) {
	bytes := append([]byte(nil), documentPDF...)
	req := documentRequest(model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: bytes})
	snapshot, err := agenttest.NewRequest("openai", req)
	require.NoError(t, err)
	bytes[0] = '!'
	req.Messages[0].Parts = nil
	replayed, err := snapshot.Request()
	require.NoError(t, err)
	facts, err := contextmeasure.Measure(replayed, model.ContextLimits{}, replayed.Messages)
	require.NoError(t, err)
	assert.Equal(t, 4, facts.TextBytes)
	assert.Equal(t, 1, len(documentPDF)%2, "fixture exercises odd-byte rounding")
	assert.Equal(t, (len(documentPDF)+1)/2, facts.NonText)
	assert.Equal(t, facts.NonText, facts.ViewNonText)
	transport := &documentTransport{}
	client, err := model.NewClient(newDocumentProvider(t, transport))
	require.NoError(t, err)
	_, err = client.Complete(t.Context(), replayed)
	require.NoError(t, err)
	var body struct {
		Input []struct {
			Content []struct {
				Filename string `json:"filename"`
				FileData string `json:"file_data"`
			} `json:"content"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(transport.request, &body))
	require.Len(t, body.Input, 1)
	require.Len(t, body.Input[0].Content, 1)
	assert.Equal(t, "spec", body.Input[0].Content[0].Filename)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(body.Input[0].Content[0].FileData, "data:application/pdf;base64,"))
	require.NoError(t, err)
	assert.Equal(t, documentPDF, decoded)
}

func newDocumentProvider(t *testing.T, transport *documentTransport) *openaimodel.Client {
	t.Helper()
	sdk := openai.NewClient(option.WithAPIKey("test"), option.WithMaxRetries(0), option.WithHTTPClient(&http.Client{Transport: transport}))
	provider, err := openaimodel.New(openaimodel.Options{Client: &sdk.Responses, DefaultModel: "gpt-4o"})
	require.NoError(t, err)
	return provider
}

func documentRequest(part model.DocumentPart) *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{part}}}}
}

func (r *documentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	r.request = data
	body := `{"id":"resp_pdf","object":"response","status":"completed","model":"gpt-4o","output":[{"type":"message","id":"msg_pdf","role":"assistant","content":[{"type":"output_text","text":"Read the PDF."}]}]}`
	contentType := "application/json"
	if r.stream {
		body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
