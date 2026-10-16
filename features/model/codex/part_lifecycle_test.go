package codex

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexPartLifecycleReconcilesWithoutDuplicateContent(t *testing.T) {
	tests := []struct {
		name, item, added, done string
		part                    model.Part
	}{
		{"text", `{"id":"m","type":"message","content":[{"type":"output_text","text":"answer"}]}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text"}}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"answer"}}`, model.TextPart{Text: "answer"}},
		{"refusal", `{"id":"m","type":"message","content":[{"type":"refusal","refusal":"declined"}]}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"refusal"}}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"declined"}}`, model.TextPart{Text: "declined"}},
		{"reasoning", `{"id":"m","type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]}`, `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`, `{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"reason"}}`, model.ThinkingPart{Text: "reason", Final: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newEventsClient(t,
				`{"type":"response.queued","response":{"id":"resp","status":"queued"}}`,
				`{"type":"response.in_progress","response":{"id":"resp","status":"in_progress"}}`,
				`{"type":"response.output_item.added","output_index":0,"item":`+tt.item+`}`,
				tt.added, tt.done, tt.done,
				`{"type":"response.output_item.done","output_index":0,"item":`+tt.item+`}`,
				`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[`+tt.item+`]}}`)
			response, err := client.Complete(t.Context(), testRequest())
			require.NoError(t, err)
			require.Len(t, response.Content, 1)
			assert.Equal(t, []model.Part{tt.part}, response.Content[0].Parts)
		})
	}
}

func TestCodexRejectsInconsistentPartFinalization(t *testing.T) {
	message := `{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message"}}`
	reasoning := `{"type":"response.output_item.added","output_index":0,"item":{"id":"r","type":"reasoning"}}`
	tool := `{"type":"response.output_item.added","output_index":0,"item":{"id":"t","type":"function_call","name":"tool","call_id":"call"}}`
	tests := []struct {
		name   string
		events []string
	}{
		{"text delta after item done", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[]}}`, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"new"}`}},
		{"text done without index", []string{message, `{"type":"response.output_text.done","output_index":0,"text":"new"}`}},
		{"tool delta after item done", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"t","type":"function_call","name":"tool","call_id":"call","arguments":"{}"}}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{}"}`}},
		{"unsupported completed item", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"x","type":"future"}}`}},
		{"text completion changes delta", []string{message, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"first"}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"other"}]}}`}},
		{"summary completion changes delta", []string{reasoning, `{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"first"}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"r","type":"reasoning","summary":[{"type":"summary_text","text":"other"}]}}`}},
		{"reasoning completion changes delta", []string{reasoning, `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"first"}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"r","type":"reasoning","content":[{"type":"reasoning_text","text":"other"}]}}`}},
		{"reasoning delta on message", []string{message, `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"wrong"}`}},
		{"reasoning done negative index", []string{reasoning, `{"type":"response.reasoning_text.done","output_index":0,"content_index":-1,"text":"wrong"}`}},
		{"new reasoning completion after item done", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"r","type":"reasoning","summary":[]}}`, `{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"new"}`}},
		{"terminal missing identity", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"message","content":[]}]}}`}},
		{"terminal unsupported item", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"x","type":"future"}]}}`}},
		{"negative input_tokens", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","usage":{"input_tokens":-1}}}`}},
		{"negative output_tokens", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","usage":{"output_tokens":-1}}}`}},
		{"negative total_tokens", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","usage":{"total_tokens":-1}}}`}},
		{"cache exceeds input", []string{`{"type":"response.completed","response":{"id":"resp","status":"completed","usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":2}}}}`}},
		{"missing index", []string{`{"type":"response.output_item.added","item":{"id":"m","type":"message"}}`}},
		{"unsupported item", []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"future"}}`}},
		{"tool delta missing identity", []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"t","type":"function_call"}}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{}"}`}},
		{"tool done missing identity", []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"t","type":"function_call"}}`, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{}"}`}},
		{"changed text completion", []string{message, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"one"}`, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"two"}`}},
		{"changed text kind at completion", []string{message, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"one"}`, `{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"one"}`}},
		{"changed reasoning completion", []string{reasoning, `{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"one"}`, `{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"two"}`}},
		{"changed tool completion", []string{tool, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{}"}`, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"x\":1}"}`}},
		{"unfinished text part", []string{message, `{"type":"response.output_text.delta","output_index":0,"content_index":1,"delta":"omitted"}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"one"}]}}`}},
		{"unfinished reasoning part", []string{reasoning, `{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":1,"delta":"omitted"}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"r","type":"reasoning","summary":[{"type":"summary_text","text":"one"}]}}`}},
		{"changed item completion", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"one"}]}}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[{"type":"output_text","text":"two"}]}}`}},
		{"reasoning after item completion", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"r","type":"reasoning","summary":[]}}`, `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text"}}`}},
		{"text after item completion", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","content":[]}}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text"}}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := newEventsClient(t, tt.events...).Complete(t.Context(), testRequest())
			requireInvalidStreamError(t, err)
			assert.Nil(t, response)
		})
	}
}
