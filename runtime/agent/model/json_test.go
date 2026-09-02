package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

func FuzzMessageJSONCodec(f *testing.F) {
	f.Add([]byte(`{"Role":"assistant","Parts":[{"Kind":"text","Text":"hello"}],"Meta":{"trace":"trace-1"}}`))
	f.Add([]byte(`{"Role":"user","Parts":["legacy text"]}`))
	f.Add([]byte(`{"Role":"assistant","Parts":[{"Kind":"tool_use","ID":"call-1","Name":"search","Input":{"q":"loom"}}]}`))
	f.Add([]byte(`{"Role":"assistant","Parts":[{"Kind":"audio"}]}`))
	f.Add([]byte(`{"Role":"assistant","Parts":[null]}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		var decoded Message
		err := json.Unmarshal(data, &decoded)
		if !jsontext.Value(data).IsValid() {
			require.Error(t, err)
			return
		}
		if err != nil {
			return
		}
		encoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		var roundTrip Message
		require.NoError(t, json.Unmarshal(encoded, &roundTrip))
		assert.Equal(t, decoded, roundTrip)
	})
}

func TestMessageJSONRoundTripPreservesEveryPartType(t *testing.T) {
	documentLocation := &DocumentPageLocation{DocumentIndex: 2, Start: 4, End: 5}
	original := Message{
		Role: ConversationRoleAssistant,
		Parts: []Part{
			ThinkingPart{Text: "reason", Signature: "sig", Redacted: []byte{1, 2}, Index: 1, Final: true},
			TextPart{Text: "answer"},
			ImagePart{Format: ImageFormatPNG, Bytes: []byte{3, 4}},
			DocumentPart{Name: "spec", Format: DocumentFormatMD, Chunks: []string{"one", "two"}, Context: "reference", Cite: true},
			CitationsPart{Text: "supported", Citations: []Citation{{
				Title: "spec", Source: "upload", Location: CitationLocation{DocumentPage: documentLocation}, SourceContent: []string{"quote"},
			}}},
			ToolUsePart{ID: "call-1", Name: "search", Input: map[string]any{"q": "loom", "exact": true}},
			ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"answer": "found"}, IsError: false},
			CacheCheckpointPart{},
		},
		Meta: map[string]any{"trace": "trace-1"},
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, original, decoded)
}

func TestMessageMarshalAcceptsPointersToEveryPartType(t *testing.T) {
	parts := []Part{
		&ThinkingPart{Text: "reason"},
		&TextPart{Text: "answer"},
		&ImagePart{Format: ImageFormatJPEG, Bytes: []byte{1}},
		&DocumentPart{Name: "doc", Text: "contents"},
		&CitationsPart{Text: "cited"},
		&ToolUsePart{Name: "search"},
		&ToolResultPart{ToolUseID: "call-1"},
		&CacheCheckpointPart{},
	}

	raw, err := json.Marshal(Message{Role: ConversationRoleUser, Parts: parts})
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(raw, &decoded))
	expected := []Part{
		ThinkingPart{Text: "reason"},
		TextPart{Text: "answer"},
		ImagePart{Format: ImageFormatJPEG, Bytes: []byte{1}},
		DocumentPart{Name: "doc", Text: "contents"},
		CitationsPart{Text: "cited"},
		ToolUsePart{Name: "search"},
		ToolResultPart{ToolUseID: "call-1"},
		CacheCheckpointPart{},
	}
	assert.Equal(t, expected, decoded.Parts)
}

func TestMessageMarshalRejectsInvalidParts(t *testing.T) {
	cases := []struct {
		name    string
		part    Part
		message string
	}{
		{name: "nil_thinking", part: (*ThinkingPart)(nil), message: "nil ThinkingPart"},
		{name: "nil_text", part: (*TextPart)(nil), message: "nil TextPart"},
		{name: "nil_image", part: (*ImagePart)(nil), message: "nil ImagePart"},
		{name: "nil_document", part: (*DocumentPart)(nil), message: "nil DocumentPart"},
		{name: "nil_citations", part: (*CitationsPart)(nil), message: "nil CitationsPart"},
		{name: "nil_tool_use", part: (*ToolUsePart)(nil), message: "nil ToolUsePart"},
		{name: "nil_tool_result", part: (*ToolResultPart)(nil), message: "nil ToolResultPart"},
		{name: "nil_cache_checkpoint", part: (*CacheCheckpointPart)(nil), message: "nil CacheCheckpointPart"},
		{name: "image_without_format", part: ImagePart{Bytes: []byte{1}}, message: "requires Format"},
		{name: "image_without_bytes", part: ImagePart{Format: ImageFormatPNG}, message: "requires Bytes"},
		{name: "document_without_name", part: DocumentPart{Text: "contents"}, message: "requires Name"},
		{name: "document_without_source", part: DocumentPart{Name: "doc"}, message: "exactly one"},
		{name: "document_with_multiple_sources", part: DocumentPart{Name: "doc", Text: "contents", URI: "s3://bucket/doc"}, message: "exactly one"},
		{name: "document_with_empty_chunk", part: DocumentPart{Name: "doc", Chunks: []string{""}}, message: "non-empty Chunks[0]"},
		{name: "tool_use_without_name", part: ToolUsePart{ID: "call-1"}, message: "requires Name"},
		{name: "tool_result_without_id", part: ToolResultPart{Content: "result"}, message: "requires ToolUseID"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := json.Marshal(Message{Parts: []Part{tt.part}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestDecodeMessagePartSupportsLegacyShapes(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		expected Part
	}{
		{name: "raw_text", payload: `"hello"`, expected: TextPart{Text: "hello"}},
		{name: "text_object", payload: `{"Text":"hello"}`, expected: TextPart{Text: "hello"}},
		{name: "thinking", payload: `{"Text":"reason","Signature":"sig"}`, expected: ThinkingPart{Text: "reason", Signature: "sig"}},
		{name: "tool_use", payload: `{"ID":"call-1","Name":"search","Args":{"q":"loom"}}`, expected: ToolUsePart{ID: "call-1", Name: "search", Input: map[string]any{"q": "loom"}}},
		{name: "tool_result", payload: `{"ToolUseID":"call-1","Content":"found"}`, expected: ToolResultPart{ToolUseID: "call-1", Content: "found"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			part, err := decodeMessagePart([]byte(tt.payload))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, part)
		})
	}
}

func TestMessageUnmarshalRejectsMalformedParts(t *testing.T) {
	cases := []struct {
		name    string
		part    string
		message string
	}{
		{name: "null", part: `null`, message: "empty part payload"},
		{name: "array", part: `[]`, message: "decode part object"},
		{name: "unknown_shape", part: `{"Other":true}`, message: "unknown part shape"},
		{name: "non_string_kind", part: `{"Kind":1}`, message: "decode Kind"},
		{name: "unknown_kind", part: `{"Kind":"audio"}`, message: `unknown part kind "audio"`},
		{name: "invalid_image", part: `{"Kind":"image","Format":"png"}`, message: "requires Bytes"},
		{name: "invalid_document", part: `{"Kind":"document","Name":"doc"}`, message: "exactly one"},
		{name: "invalid_tool_use", part: `{"Kind":"tool_use"}`, message: "requires Name"},
		{name: "invalid_tool_result", part: `{"Kind":"tool_result"}`, message: "requires ToolUseID"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var message Message
			err := json.Unmarshal([]byte(`{"Role":"user","Parts":[`+tt.part+`]}`), &message)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestPartMarshalJSONIncludesKind(t *testing.T) {
	cases := []struct {
		name string
		part Part
		kind string
	}{
		{
			name: "thinking",
			part: ThinkingPart{
				Text:      "think",
				Signature: "sig",
				Index:     1,
				Final:     true,
			},
			kind: "thinking",
		},
		{name: "text", part: TextPart{Text: "hello"}, kind: "text"},
		{name: "image", part: ImagePart{Format: ImageFormatPNG, Bytes: []byte{0x01}}, kind: "image"},
		{name: "document", part: DocumentPart{Name: "doc", Format: DocumentFormatTXT, Text: "hello"}, kind: "document"},
		{name: "citations", part: CitationsPart{Text: "supported", Citations: []Citation{{Title: "t"}}}, kind: "citations"},
		{name: "tool_use", part: ToolUsePart{Name: "search", Input: map[string]any{"q": "golang"}}, kind: "tool_use"},
		{name: "tool_result", part: ToolResultPart{ToolUseID: "tu", Content: map[string]any{"hits": 1}}, kind: "tool_result"},
		{name: "cache_checkpoint", part: CacheCheckpointPart{}, kind: "cache_checkpoint"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(Message{
				Role:  ConversationRoleUser,
				Parts: []Part{tt.part},
			})
			require.NoError(t, err)
			var obj map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(raw, &obj))

			var parts []jsontext.Value
			require.NoError(t, json.Unmarshal(obj["Parts"], &parts))
			require.Len(t, parts, 1)

			var partObj map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(parts[0], &partObj))

			var kind string
			require.NoError(t, json.Unmarshal(partObj["Kind"], &kind))
			require.Equal(t, tt.kind, kind)
		})
	}
}

func TestDecodeMessagePartHonorsKind(t *testing.T) {
	const payload = `{"Kind":"tool_use","Name":"legacy","Args":{"q":"old"}}`
	part, err := decodeMessagePart([]byte(payload))
	require.NoError(t, err)

	tu, ok := part.(ToolUsePart)
	require.True(t, ok)
	require.Equal(t, "legacy", tu.Name)
	require.Equal(t, map[string]any{"q": "old"}, tu.Input)
}

func TestThinkingPartRoundTripPreservesSignature(t *testing.T) {
	orig := ThinkingPart{
		Text:      "let me think",
		Signature: "signed-by-provider",
		Redacted:  []byte{0x01, 0x02},
		Index:     3,
		Final:     true,
	}

	raw, err := json.Marshal(orig)
	require.NoError(t, err)

	part, err := decodeMessagePart(raw)
	require.NoError(t, err)

	got, ok := part.(ThinkingPart)
	require.True(t, ok)
	require.Equal(t, orig.Text, got.Text)
	require.Equal(t, orig.Signature, got.Signature)
	require.Equal(t, orig.Index, got.Index)
	require.Equal(t, orig.Final, got.Final)
	require.Equal(t, orig.Redacted, got.Redacted)
}

func TestCacheCheckpointPartRoundTrip(t *testing.T) {
	orig := CacheCheckpointPart{}

	raw, err := json.Marshal(Message{
		Role:  ConversationRoleUser,
		Parts: []Part{orig},
	})
	require.NoError(t, err)

	var obj map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(raw, &obj))

	var parts []jsontext.Value
	require.NoError(t, json.Unmarshal(obj["Parts"], &parts))
	require.Len(t, parts, 1)

	// Verify it emits a Kind discriminator, not an empty object.
	require.JSONEq(t, `{"Kind":"cache_checkpoint"}`, string(parts[0]))

	part, err := decodeMessagePart(parts[0])
	require.NoError(t, err)

	_, ok := part.(CacheCheckpointPart)
	require.True(t, ok, "expected CacheCheckpointPart, got %T", part)
}

func TestDecodeEmptyObjectReturnsError(t *testing.T) {
	_, err := decodeMessagePart([]byte(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty part payload")
}

func TestDocumentPartDecodeRejectsInvalidSources(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "missing_source",
			payload: `{"Kind":"document","Name":"doc"}`,
		},
		{
			name:    "multiple_sources",
			payload: `{"Kind":"document","Name":"doc","Text":"a","URI":"s3://b/doc.pdf"}`,
		},
		{
			name:    "empty_chunk",
			payload: `{"Kind":"document","Name":"doc","Chunks":[""]}`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeMessagePart([]byte(tt.payload))
			require.Error(t, err)
		})
	}
}

func TestRequestJSONRoundTripPreservesPromptRefs(t *testing.T) {
	original := &Request{
		RunID: "run-123",
		PromptRefs: []prompt.PromptRef{
			{
				ID:      "planner.system",
				Version: "v1",
			},
			{
				ID:      "planner.tool",
				Version: "v3",
			},
		},
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Request
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, original.PromptRefs, decoded.PromptRefs)
}
