package recall

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalDocumentText(t *testing.T) {
	f := newRecallFixture(t)
	f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{
		model.TextPart{Text: "before"}, model.DocumentPart{Name: "text", Format: model.DocumentFormatTXT, Text: strings.Repeat("document ", 100)}, model.DocumentPart{Name: "text", Format: model.DocumentFormatTXT, Chunks: []string{"chunk one", "chunk two"}},
	}})})
	first := f.call(`{"read":{"id":"1","part":"result","offset":0}}`)
	assert.Contains(t, first.Text, "before\ndocument")
	assert.NotNil(t, first.NextOffset)
	assert.Empty(t, first.Code)
	f.req.View.TailFrom = 2
	assert.NotEmpty(t, f.call(`{"find":{"text":"chunk two"}}`).Matches)
}
func TestMultimodalInputAndSpec(t *testing.T) {
	_, err := ParseInput([]byte(`{"read":{"id":"1","part":"attachment","index":2,"offset":0}}`))
	require.NoError(t, err)
	assert.NotContains(t, Description, "until supported")
	for _, input := range []string{
		`{"read":{"id":"1","part":"attachment","offset":0}}`,
		`{"read":{"id":"1","part":"attachment","index":0,"offset":0}}`,
		`{"read":{"id":"1","part":"attachment","index":2,"offset":1}}`,
		`{"read":{"id":"1","part":"result","index":2,"offset":0}}`,
	} {
		_, err := ParseInput([]byte(input))
		assert.Error(t, err, input)
	}
}

func TestMultimodalAttachments(t *testing.T) {
	for _, tc := range []struct {
		name, reason, unit string
		support            model.ToolResultPartSupport
		tokens             int
	}{
		{name: "supported", support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, MaxImageBytes: 100}, tokens: 1500},
		{name: "provider", reason: "provider", tokens: 1500},
		{name: "bytes", reason: "too_large", unit: "bytes", support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, MaxImageBytes: 1}, tokens: 1500},
		{name: "tokens", reason: "too_large", unit: "tokens", support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, MaxImageBytes: 100}, tokens: 1499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			f.req.Parts, f.req.PartTokens = tc.support, tc.tokens
			img := model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}
			f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "text"}, img, model.DocumentPart{Name: "pdf", Format: model.DocumentFormatPDF, Bytes: []byte{4, 5}}}})})
			response := f.call(`{"read":{"id":"1","part":"result","offset":0}}`)
			assert.Len(t, response.Attachments, 2)
			assert.Equal(t, 2, response.Attachments[0].Part)
			assert.Empty(t, f.call(`{"read":{"id":"1","part":"result","offset":1}}`).Attachments)
			result := f.call(`{"read":{"id":"1","part":"attachment","index":2,"offset":0}}`)
			assert.Equal(t, tc.reason, result.Reason)
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(data, &wire))
			if tc.unit == "" {
				assert.NotContains(t, wire, "limit_unit")
			} else {
				assert.Equal(t, tc.unit, wire["limit_unit"])
			}
			if tc.reason == "" {
				assert.Empty(t, result.Code)
				assert.Equal(t, []model.ToolResultContentPart{img}, result.Parts)
				assert.Equal(t, 2, result.Attachment.Part)
			} else {
				assert.Equal(t, CodeUnsupportedPart, result.Code)
				assert.Empty(t, result.Parts)
			}
			assert.Equal(t, CodeNotFound, f.call(`{"read":{"id":"1","part":"attachment","index":1,"offset":0}}`).Code)
			assert.Equal(t, CodeNotFound, f.call(`{"read":{"id":"1","part":"attachment","index":9,"offset":0}}`).Code)
		})
	}
}

func TestMultimodalDocumentLimits(t *testing.T) {
	for _, tc := range []struct {
		name          string
		document      model.DocumentPart
		reason, unit  string
		tokens, limit int
	}{
		{name: "URI", document: model.DocumentPart{Name: "remote", Format: model.DocumentFormatPDF, URI: "https://example.com/doc.pdf"}, reason: "uri", tokens: 1500},
		{name: "document token limit", document: model.DocumentPart{Name: "bytes", Format: model.DocumentFormatPDF, Bytes: []byte{1, 2, 3, 4, 5}}, reason: "too_large", unit: "tokens", tokens: 2, limit: 2},
		{name: "byte limit", document: model.DocumentPart{Name: "bytes", Format: model.DocumentFormatPDF, Bytes: make([]byte, 101)}, reason: "too_large", unit: "bytes", tokens: 1500, limit: 100},
		{name: "S3", document: model.DocumentPart{Name: "remote", Format: model.DocumentFormatPDF, URI: "s3://bucket/key", Cite: true}, tokens: 1500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			f.req.Parts = model.ToolResultPartSupport{DocumentFormats: []model.DocumentFormat{model.DocumentFormatPDF}, DocumentURISchemes: []string{"s3"}, MaxDocumentBytes: 100}
			f.req.PartTokens = tc.tokens
			f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{tc.document}})})
			out := f.call(`{"read":{"id":"1","part":"attachment","index":1,"offset":0}}`)
			assert.Equal(t, tc.reason, out.Reason)
			assert.Equal(t, tc.limit, out.Limit)
			assert.Equal(t, tc.unit, out.LimitUnit)
			if tc.reason == "" {
				require.Len(t, out.Parts, 1)
				assert.False(t, out.Parts[0].(model.DocumentPart).Cite)
			} else {
				assert.Empty(t, out.Parts)
			}
		})
	}
}

func TestMultimodalRecallNeverReadsResultPartsOrServerData(t *testing.T) {
	f := newRecallFixture(t)
	secret := f.raw([]byte("server only"))
	f.h.Store = guardedStore{Store: f.store, forbidden: secret, t: t}
	f.entries(index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(`{"attachment":{"message":"1"}}`)), ToolUseIDs: []string{"recall"}, Result: &index.ResultProjection{Parts: secret}, Record: []runstore.Hash{secret}, Output: secret})
	assert.Equal(t, CodeNotFound, f.call(`{"read":{"id":"1","part":"attachment","index":1,"offset":0}}`).Code)
	result := f.call(`{"read":{"id":"1","part":"result","offset":0}}`)
	assert.Contains(t, result.Text, "message")
	assert.Empty(t, result.Parts)
	assert.Empty(t, result.Attachments)
}

func TestMultimodalURICeiling(t *testing.T) {
	for _, cap := range []int{0, 2} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			f := newRecallFixture(t)
			f.req.Parts = model.ToolResultPartSupport{DocumentURISchemes: []string{"s3"}, MaxDocumentBytes: cap}
			f.req.PartTokens = 1500
			f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.DocumentPart{Name: "remote", Format: model.DocumentFormatPDF, URI: "s3://bucket/key"}}})})
			out := f.call(`{"read":{"id":"1","part":"attachment","index":1,"offset":0}}`)
			require.Empty(t, out.Code)
			require.Len(t, out.Parts, 1)
			assert.Zero(t, out.Attachment.Bytes)
			assert.Equal(t, 1500, out.Attachment.Tokens)
			f.req.PartTokens = 1499
			out = f.call(`{"read":{"id":"1","part":"attachment","index":1,"offset":0}}`)
			assert.Equal(t, CodeUnsupportedPart, out.Code)
			assert.Equal(t, "too_large", out.Reason)
			assert.Equal(t, 1499, out.Limit)
			data, err := json.Marshal(out)
			require.NoError(t, err)
			assert.JSONEq(t, `{"code":"recall_unsupported_part","format":"pdf","reason":"too_large","limit":1499,"limit_unit":"tokens"}`, string(data))
		})
	}
}

func TestMultimodalRejectionSchema(t *testing.T) {
	var schema any
	require.NoError(t, json.Unmarshal(Spec().Result.Schema, &schema))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("result.json", schema))
	compiled, err := compiler.Compile("result.json")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, wire string
		valid      bool
	}{
		{"bytes", `{"code":"recall_unsupported_part","reason":"too_large","limit":2,"limit_unit":"bytes"}`, true},
		{"tokens", `{"code":"recall_unsupported_part","reason":"too_large","limit":1499,"limit_unit":"tokens"}`, true},
		{"missing unit", `{"code":"recall_unsupported_part","reason":"too_large","limit":2}`, false},
		{"invalid unit", `{"code":"recall_unsupported_part","reason":"too_large","limit":2,"limit_unit":"characters"}`, false},
		{"provider", `{"code":"recall_unsupported_part","reason":"provider"}`, true},
		{"uri", `{"code":"recall_unsupported_part","reason":"uri"}`, true},
		{"success", `{"attachment":{"message":"1"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			require.NoError(t, json.Unmarshal([]byte(tc.wire), &value))
			err := compiled.Validate(value)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
