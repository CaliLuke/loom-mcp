package model

import (
	"io"
	"math"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestContractRejectsInvalidCatalogAndSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *Request
		want string
	}{
		{"nil tool", &Request{Tools: []*ToolDefinition{nil}}, "nil"},
		{"empty tool name", &Request{Tools: []*ToolDefinition{{InputSchema: rawjson.Message(`{}`)}}}, "name is required"},
		{"padded tool name", &Request{Tools: []*ToolDefinition{{Name: " lookup ", InputSchema: rawjson.Message(`{}`)}}}, "whitespace"},
		{"duplicate tool", &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{}`)}, {Name: "lookup", InputSchema: rawjson.Message(`{}`)}}}, "duplicated"},
		{"missing schema", &Request{Tools: []*ToolDefinition{{Name: "lookup"}}}, "schema"},
		{"invalid schema", &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{"type": "invalid"}`)}}}, "schema"},
		{"malformed schema", &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{`)}}}, "schema"},
		{"structured tools", &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{}`)}}, StructuredOutput: &StructuredOutput{Name: "result", Schema: rawjson.Message(`{}`)}}, "cannot combine"},
		{"structured empty name", &Request{StructuredOutput: &StructuredOutput{Schema: rawjson.Message(`{}`)}}, "name is required"},
		{"structured padded name", &Request{StructuredOutput: &StructuredOutput{Name: " result ", Schema: rawjson.Message(`{}`)}}, "whitespace"},
		{"structured invalid schema", &Request{StructuredOutput: &StructuredOutput{Name: "result", Schema: rawjson.Message(`{"type":"invalid"}`)}}, "schema"},
		{"any with name", &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{}`)}}, ToolChoice: &ToolChoice{Mode: ToolChoiceModeAny, Name: "lookup"}}, "cannot name"},
		{"unknown choice", &Request{ToolChoice: &ToolChoice{Mode: "invalid"}}, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract, err := NewRequestContract(tc.req)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, contract)
		})
	}
}

func TestValidatedStreamRejectsInconsistentProviderSequence(t *testing.T) {
	call := ToolCallChunk{ToolCall: ToolCall{Name: "lookup", ID: "call", Payload: rawjson.Message(`{}`)}}
	other := ToolCallChunk{ToolCall: ToolCall{Name: "other", ID: "call", Payload: rawjson.Message(`{}`)}}
	delta := ToolCallDeltaChunk{Delta: ToolCallDelta{Name: "lookup", ID: "call", Delta: `{}`}}
	completion := CompletionChunk{Completion: Completion{Name: "result", Payload: rawjson.Message(`{}`)}}
	text := TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "hello"}}}}
	for _, tc := range []struct {
		name       string
		structured bool
		chunks     []Chunk
		kind       OutputValidationKind
	}{
		{"repeated finalized call", false, []Chunk{call, call}, OutputValidationStreamProtocol},
		{"changed final name", false, []Chunk{delta, other}, OutputValidationStreamProtocol},
		{"changed final arguments", false, []Chunk{ToolCallDeltaChunk{Delta: ToolCallDelta{Name: "lookup", ID: "call", Delta: `{"x":1}`}}, call}, OutputValidationStreamProtocol},
		{"delta after final", false, []Chunk{call, delta}, OutputValidationStreamProtocol},
		{"changed delta name", false, []Chunk{delta, ToolCallDeltaChunk{Delta: ToolCallDelta{Name: "other", ID: "call", Delta: `{}`}}}, OutputValidationStreamProtocol},
		{"unadvertised delta", false, []Chunk{ToolCallDeltaChunk{Delta: ToolCallDelta{Name: "unknown", ID: "call", Delta: `{}`}}}, OutputValidationToolIdentity},
		{"delta without identifier", false, []Chunk{ToolCallDeltaChunk{Delta: ToolCallDelta{Name: "lookup", Delta: `{}`}}}, OutputValidationStreamProtocol},
		{"unfinished delta", false, []Chunk{delta, StopChunk{Reason: "tool_use"}}, OutputValidationStreamProtocol},
		{"completion without request", false, []Chunk{completion}, OutputValidationStructuredOutput},
		{"completion delta without request", false, []Chunk{CompletionDeltaChunk{Delta: CompletionDelta{Name: "result", Delta: `{}`}}}, OutputValidationStructuredOutput},
		{"structured text", true, []Chunk{text}, OutputValidationStructuredOutput},
		{"structured tool", true, []Chunk{call}, OutputValidationStructuredOutput},
		{"structured tool delta", true, []Chunk{delta}, OutputValidationStructuredOutput},
		{"repeated completion", true, []Chunk{completion, completion}, OutputValidationStreamProtocol},
		{"wrong completion", true, []Chunk{CompletionChunk{Completion: Completion{Name: "other", Payload: rawjson.Message(`{}`)}}}, OutputValidationStructuredOutput},
		{"wrong completion delta", true, []Chunk{CompletionDeltaChunk{Delta: CompletionDelta{Name: "other", Delta: `{}`}}}, OutputValidationStructuredOutput},
		{"missing completion", true, []Chunk{StopChunk{Reason: "end_turn"}}, OutputValidationStructuredOutput},
		{"invalid usage", false, []Chunk{UsageChunk{Usage: TokenUsage{OutputTokens: -1}}}, OutputValidationUsage},
		{"changed usage model", false, []Chunk{UsageChunk{Usage: TokenUsage{Model: "a"}}, UsageChunk{Usage: TokenUsage{Model: "b"}}}, OutputValidationUsage},
		{"changed usage class", false, []Chunk{UsageChunk{Usage: TokenUsage{ModelClass: ModelClassSmall}}, UsageChunk{Usage: TokenUsage{ModelClass: ModelClassHighReasoning}}}, OutputValidationUsage},
		{"usage overflows", false, []Chunk{UsageChunk{Usage: TokenUsage{InputTokens: math.MaxInt}}, UsageChunk{Usage: TokenUsage{InputTokens: 1}}}, OutputValidationUsage},
		{"empty stop reason", false, []Chunk{StopChunk{}}, OutputValidationStreamProtocol},
		{"empty completion payload", true, []Chunk{CompletionChunk{Completion: Completion{Name: "result"}}}, OutputValidationStructuredOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &Request{Tools: []*ToolDefinition{{Name: "lookup", InputSchema: rawjson.Message(`{}`)}, {Name: "other", InputSchema: rawjson.Message(`{}`)}}}
			if tc.structured {
				request = &Request{StructuredOutput: &StructuredOutput{Name: "result", Schema: rawjson.Message(`{}`)}}
			}
			contract, err := NewRequestContract(request)
			require.NoError(t, err)
			provider := &contractStream{chunks: tc.chunks}
			stream, err := contract.ValidateStream(provider)
			require.NoError(t, err)
			for range len(tc.chunks) + 1 {
				chunk, recvErr := stream.Recv()
				if recvErr != nil {
					err = recvErr
					break
				}
				switch chunk.(type) {
				case ToolCallChunk, CompletionChunk:
					t.Fatal("unvalidated terminal output escaped")
				}
			}
			require.Error(t, err)
			require.NotErrorIs(t, err, io.EOF)
			var validation *OutputValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, tc.kind, validation.Kind())
			assert.Nil(t, stream.Response())
			require.ErrorIs(t, stream.Finalize(nil), err)
			assert.Equal(t, 1, provider.closed)
			_, again := stream.Recv()
			require.Error(t, again)
		})
	}
}

func TestRequestContractRejectsMissingProviderStream(t *testing.T) {
	contract, err := NewRequestContract(&Request{})
	require.NoError(t, err)
	var typedNil *contractStream
	for _, provider := range []Streamer{nil, typedNil} {
		stream, err := contract.ValidateStream(provider)
		require.Error(t, err)
		assert.Nil(t, stream)
	}
}

func TestValidatedStreamRejectsOversizedChunkBeforePublishing(t *testing.T) {
	oversized := strings.Repeat("x", (16<<20)+1)
	for _, tc := range []struct {
		name  string
		chunk Chunk
	}{
		{"text", TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: oversized}}}}},
		{"thinking", ThinkingChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{ThinkingPart{Text: oversized}}}}},
		{"tool", ToolCallChunk{ToolCall: ToolCall{ID: "call", Name: "read", Payload: rawjson.Message(oversized)}}},
		{"tool delta", ToolCallDeltaChunk{Delta: ToolCallDelta{ID: "call", Name: "read", Delta: oversized}}},
		{"completion", CompletionChunk{Completion: Completion{Name: "result", Payload: rawjson.Message(oversized)}}},
		{"completion delta", CompletionDeltaChunk{Delta: CompletionDelta{Name: "result", Delta: oversized}}},
		{"stop", StopChunk{Reason: oversized}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract, err := NewRequestContract(&Request{})
			require.NoError(t, err)
			provider := &contractStream{chunks: []Chunk{tc.chunk}}
			stream, err := contract.ValidateStream(provider)
			require.NoError(t, err)
			chunk, err := stream.Recv()
			var validation *OutputValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, OutputValidationOutputBounds, validation.Kind())
			assert.Nil(t, chunk)
			assert.Nil(t, stream.Response())
			require.ErrorIs(t, stream.Finalize(nil), err)
			assert.Equal(t, 1, provider.closed)
		})
	}
}
