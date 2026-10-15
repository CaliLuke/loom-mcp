package model

import (
	"math"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneRequestEnforcesAggregateByteBudget(t *testing.T) {
	oversized := strings.Repeat("a", (16<<20)+1)
	for _, tc := range []struct {
		name    string
		request Request
	}{
		{"identity", Request{RunID: oversized}},
		{"prompt provenance", Request{PromptRefs: []prompt.PromptRef{{Version: oversized}}}},
		{"tool description", Request{Tools: []*ToolDefinition{{Name: "read", Description: oversized}}}},
		{"tool choice", Request{ToolChoice: &ToolChoice{Name: oversized}}},
		{"schema", Request{StructuredOutput: &StructuredOutput{Schema: rawjson.Message(oversized)}}},
		{"metadata key", Request{Messages: []*Message{{Meta: map[string]any{oversized: true}}}}},
		{"metadata string", Request{Messages: []*Message{{Meta: map[string]any{"key": oversized}}}}},
		{"image", Request{Messages: []*Message{{Parts: []Part{ImagePart{Format: ImageFormatPNG, Bytes: []byte(oversized)}}}}}},
		{"document", Request{Messages: []*Message{{Parts: []Part{DocumentPart{Name: "doc", Format: DocumentFormatTXT, Text: oversized}}}}}},
		{"document chunk", Request{Messages: []*Message{{Parts: []Part{DocumentPart{Name: "doc", Format: DocumentFormatTXT, Chunks: []string{oversized}}}}}}},
		{"citation title", Request{Messages: []*Message{{Parts: []Part{CitationsPart{Citations: []Citation{{Title: oversized}}}}}}}},
		{"citation excerpt", Request{Messages: []*Message{{Parts: []Part{CitationsPart{Citations: []Citation{{SourceContent: []string{oversized}}}}}}}}},
		{"reasoning", Request{Messages: []*Message{{Parts: []Part{ThinkingPart{Signature: oversized}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owned, err := CloneRequest(&tc.request)
			require.ErrorContains(t, err, "maximum size")
			assert.Nil(t, owned)
		})
	}
}

func TestCloneRequestRejectsNonJSONOrCyclicMetadata(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	var deep any = "leaf"
	for range 66 {
		deep = []any{deep}
	}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"channel", make(chan int), "JSON-compatible"},
		{"pointer", new(1), "JSON-compatible"},
		{"function", func() {
		}, "JSON-compatible"},
		{"struct", struct{ Value int }{1}, "JSON-compatible"},
		{"NaN", math.NaN(), "non-finite"},
		{"infinity", math.Inf(1), "non-finite"},
		{"integer map keys", map[int]string{1: "one"}, "not a string"},
		{"cycle", cyclic, "cyclic"},
		{"depth", deep, "maximum depth"},
		{"large collection", make([]any, 100001), "maximum visited values"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := CloneRequest(&Request{Messages: []*Message{{Meta: map[string]any{"value": tc.value}}}})
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
		})
	}
}

func TestCloneToolDefinitionsOwnsArraysAndPreservesNilCollections(t *testing.T) {
	schema := map[string]any{"array": [2][]byte{[]byte("one"), []byte("two")}, "nil_map": map[string]any(nil), "nil_slice": []string(nil)}
	source := []*ToolDefinition{{Name: "read", InputSchema: schema}}
	cloned, err := CloneToolDefinitions(source)
	require.NoError(t, err)
	require.Len(t, cloned, 1)
	assert.Equal(t, source, cloned)
	owned := cloned[0].InputSchema.(map[string]any)
	owned["array"].([2][]byte)[0][0] = 'X'
	assert.Equal(t, []byte("one"), schema["array"].([2][]byte)[0])
	cloned[0].Name = "changed"
	assert.Equal(t, "read", source[0].Name)
	_, err = CloneToolDefinitions([]*ToolDefinition{nil})
	require.ErrorContains(t, err, "nil")
	_, err = CloneToolDefinitions(make([]*ToolDefinition, 100001))
	require.ErrorContains(t, err, "maximum visited values")
}

func TestCloneResponseRejectsOversizedEnvelopeAndToolCalls(t *testing.T) {
	oversized := strings.Repeat("a", (16<<20)+1)
	for _, response := range []*Response{
		{StopReason: oversized},
		{ToolCalls: []ToolCall{{Name: "read", Payload: rawjson.Message(oversized)}}},
		{Content: make([]Message, 100001)},
	} {
		cloned, err := CloneResponse(response)
		require.Error(t, err)
		assert.Nil(t, cloned)
	}
	response, err := CloneResponse(nil)
	require.NoError(t, err)
	assert.Nil(t, response)
}

func TestCloneRequestRejectsOversizedNestedCollections(t *testing.T) {
	chunks := make([]string, 100001)
	for i := range chunks {
		chunks[i] = "content"
	}
	partRequest := func(part Part) Request {
		return Request{Messages: []*Message{{Parts: []Part{part}}}}
	}
	for _, tc := range []struct {
		name    string
		request Request
	}{
		{"messages", Request{Messages: make([]*Message, 100001)}},
		{"message parts", Request{Messages: []*Message{{Parts: make([]Part, 100001)}}}},
		{"document chunks", partRequest(DocumentPart{Name: "doc", Format: DocumentFormatTXT, Chunks: chunks})},
		{"citations", partRequest(CitationsPart{Citations: make([]Citation, 100001)})},
		{"citation excerpts", partRequest(CitationsPart{Citations: []Citation{{SourceContent: make([]string, 100001)}}})},
		{"tool input", partRequest(ToolUsePart{ID: "call", Name: "read", Input: make([]any, 100001)})},
		{"tool result", partRequest(ToolResultPart{ToolUseID: "call", Content: make([]any, 100001)})},
		{"tool schema", Request{Tools: []*ToolDefinition{{Name: "read", InputSchema: make([]any, 100001)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owned, err := CloneRequest(&tc.request)
			require.ErrorContains(t, err, "maximum visited values")
			assert.Nil(t, owned)
		})
	}
}

func TestCloneResponseOwnsCitationLocations(t *testing.T) {
	original := &Response{Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{CitationsPart{Text: "answer", Citations: []Citation{
		{SourceContent: []string{"first"}, Location: CitationLocation{DocumentChar: &DocumentCharLocation{DocumentIndex: 1, Start: 2, End: 3}}},
		{SourceContent: []string{"second"}, Location: CitationLocation{DocumentChunk: &DocumentChunkLocation{DocumentIndex: 4, Start: 5, End: 6}}},
		{SourceContent: []string{"third"}, Location: CitationLocation{DocumentPage: &DocumentPageLocation{DocumentIndex: 7, Start: 8, End: 9}}},
	}}}}}}
	cloned, err := CloneResponse(original)
	require.NoError(t, err)
	assert.Equal(t, original, cloned)
	citations := cloned.Content[0].Parts[0].(CitationsPart).Citations
	citations[0].Location.DocumentChar.Start = 20
	citations[1].Location.DocumentChunk.Start = 50
	citations[2].Location.DocumentPage.Start = 80
	citations[0].SourceContent[0] = "changed"
	source := original.Content[0].Parts[0].(CitationsPart).Citations
	assert.Equal(t, 2, source[0].Location.DocumentChar.Start)
	assert.Equal(t, 5, source[1].Location.DocumentChunk.Start)
	assert.Equal(t, 8, source[2].Location.DocumentPage.Start)
	assert.Equal(t, []string{"first"}, source[0].SourceContent)
}

func TestCloneRequestRejectsMissingMessageDataWithoutPartialOutput(t *testing.T) {
	for _, tt := range []struct {
		name    string
		request *Request
	}{
		{"nil request", nil},
		{"nil message", &Request{Messages: []*Message{nil}}},
		{"nil part", &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{nil}}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cloned, err := CloneRequest(tt.request)
			require.Error(t, err)
			assert.Nil(t, cloned)
		})
	}
}
