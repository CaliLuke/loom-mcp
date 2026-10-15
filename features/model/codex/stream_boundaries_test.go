package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexRejectsMalformedEventBoundaries(t *testing.T) {
	message := `{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message"}}`
	reasoning := `{"type":"response.output_item.added","output_index":0,"item":{"id":"r","type":"reasoning"}}`
	tool := `{"type":"response.output_item.added","output_index":0,"item":{"id":"t","type":"function_call","name":"tool","call_id":"call"}}`
	tests := []struct {
		name   string
		before []string
		event  string
	}{
		{"invalid json", nil, `{`},
		{"missing event type", nil, `{}`},
		{"done before terminal", nil, `[DONE]`},
		{"negative item index", nil, `{"type":"response.output_item.added","output_index":-1,"item":{"type":"message"}}`},
		{"missing item type", nil, `{"type":"response.output_item.added","output_index":0,"item":{}}`},
		{"duplicate index", []string{message}, message},
		{"duplicate id", []string{message}, `{"type":"response.output_item.added","output_index":1,"item":{"id":"m","type":"message"}}`},
		{"text missing index", []string{message}, `{"type":"response.output_text.delta","item_id":"m","output_index":0,"delta":"x"}`},
		{"text negative index", []string{message}, `{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":-1,"delta":"x"}`},
		{"text empty delta", []string{message}, `{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":""}`},
		{"text done missing value", []string{message}, `{"type":"response.output_text.done","item_id":"m","output_index":0,"content_index":0}`},
		{"text done wrong family", []string{reasoning}, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"x"}`},
		{"negative output reference", []string{message}, `{"type":"response.output_text.delta","output_index":-1,"content_index":0,"delta":"x"}`},
		{"unknown output reference", []string{message}, `{"type":"response.output_text.delta","output_index":4,"content_index":0,"delta":"x"}`},
		{"message part wrong type", []string{message}, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"image"}}`},
		{"message part wrong family", []string{reasoning}, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text"}}`},
		{"message part missing index", []string{message}, `{"type":"response.content_part.added","output_index":0,"part":{"type":"output_text"}}`},
		{"message part missing text", []string{message}, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text"}}`},
		{"reasoning part wrong family", []string{message}, `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`},
		{"reasoning part missing index", []string{reasoning}, `{"type":"response.reasoning_summary_part.added","output_index":0,"part":{"type":"summary_text"}}`},
		{"reasoning part wrong type", []string{reasoning}, `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"output_text"}}`},
		{"reasoning part missing text", []string{reasoning}, `{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`},
		{"reasoning delta empty", []string{reasoning}, `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":""}`},
		{"reasoning delta missing index", []string{reasoning}, `{"type":"response.reasoning_text.delta","output_index":0,"delta":"x"}`},
		{"reasoning done wrong family", []string{message}, `{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"x"}`},
		{"reasoning done missing index", []string{reasoning}, `{"type":"response.reasoning_text.done","output_index":0,"text":"x"}`},
		{"reasoning done missing text", []string{reasoning}, `{"type":"response.reasoning_text.done","output_index":0,"content_index":0}`},
		{"tool delta wrong family", []string{message}, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{}"}`},
		{"tool delta empty", []string{tool}, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":""}`},
		{"tool done wrong family", []string{message}, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{}"}`},
		{"tool done missing arguments", []string{tool}, `{"type":"response.function_call_arguments.done","output_index":0}`},
		{"item done missing identity", []string{message}, `{"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`},
		{"item done negative index", nil, `{"type":"response.output_item.done","output_index":-1,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"x"}]}}`},
		{"failed missing response", nil, `{"type":"response.failed"}`},
		{"failed missing identity", nil, `{"type":"response.failed","response":{"status":"failed"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := append(append([]string{}, tt.before...), tt.event)
			client := newEventsClient(t, events...)
			response, err := client.Complete(t.Context(), testRequest())
			requireInvalidStreamError(t, err)
			assert.Nil(t, response)
		})
	}
}

func TestCodexRejectsMutationsAfterPartCompletion(t *testing.T) {
	tests := []struct {
		name   string
		events []string
	}{
		{"text type changes", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message"}}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"x"}`,
			`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"y"}`,
		}},
		{"new content after item done", []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"x"}]}}`,
			`{"type":"response.output_text.done","output_index":0,"content_index":1,"text":"y"}`,
		}},
		{"text part added after done", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message"}}`,
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"x"}`,
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text"}}`,
		}},
		{"reasoning part added after done", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"r","type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"x"}`,
			`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`,
		}},
		{"completed item index changes", []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"x"}]}}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"x"}]}}`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newEventsClient(t, tt.events...)
			response, err := client.Complete(t.Context(), testRequest())
			requireInvalidStreamError(t, err)
			require.Nil(t, response)
		})
	}
}
