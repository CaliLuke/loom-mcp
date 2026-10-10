package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

func TestIndexOracleTurnConfirmationAndAwaitScript(t *testing.T) {
	o := newIndexOracle(t, []*model.Message{
		newTextAgentMessage(model.ConversationRoleSystem, "seed system"),
		nil,
		{Role: model.ConversationRoleUser, Parts: []model.Part{
			model.TextPart{Text: "seed question"},
			model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}},
			model.DocumentPart{Name: "doc", Text: "evidence"},
		}},
	})
	turn := []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{
		model.ThinkingPart{Text: "reason", Signature: "signed"}, model.TextPart{Text: "one turn"},
	}}}
	calls := []planner.ToolRequest{{Name: "test.plain", ToolCallID: generateDeterministicToolCallID("root", "turn", 1, "test.plain", 0), Payload: []byte(`{"q":"x"}`)}}
	o.assistant(turn, calls)
	o.check("workflow_turn.go admission, ID assignment, recordAssistantTurn")
	o.rt.toolSpecs["test.plain"] = newAnyJSONSpec("test.plain", "test")
	result := &planner.ToolResult{Name: calls[0].Name, ToolCallID: calls[0].ToolCallID, Result: map[string]any{"ok": true}}
	o.results(result)
	o.check("ordinary result batch")

	confirm := planner.ToolRequest{Name: "test.confirm", ToolCallID: "confirm-1", RootRunID: "root", Payload: []byte(`{}`)}
	o.assistant(nil, []planner.ToolRequest{confirm})
	batch := index.MessageID(len(o.entries) + 1)
	o.add(index.MessageEntry{Kind: index.Structural, Batch: batch, ToolUseIDs: []string{confirm.ToolCallID}, Structural: &index.StructuralEntry{
		Kind: index.ConfirmationDenial, ProtocolVersion: 5, Text: []index.TextParameter{{Text: `{"summary":"denied"}`}},
	}})
	o.check("workflow_await_queue.go confirmation pair without repeated transcript")

	questions := &planner.AwaitQuestions{ID: "q", ToolName: "test.questions", ToolCallID: "questions-1", Payload: []byte(`{}`)}
	o.assistant(nil, []planner.ToolRequest{{Name: questions.ToolName, ToolCallID: questions.ToolCallID, Payload: questions.Payload}})
	o.check("workflow_await_publication.go questions")
	o.assistant(nil, []planner.ToolRequest{
		{Name: "test.external", ToolCallID: "external-1", Payload: []byte(`{"a":1}`)},
		{Name: "test.external", ToolCallID: "external-2", Payload: []byte(`{"a":2}`)},
	})
	o.check("workflow_await_publication.go external batch")
	assert.Equal(t, 1, countAssistantTextParts(o.base.Messages, "one turn"))
}

func TestIndexOracleClarificationResumeAndTypedScript(t *testing.T) {
	o := newIndexOracle(t, nil)
	for _, answer := range []string{"clarification from await", ""} {
		ref := o.put([]byte(answer))
		o.add(index.MessageEntry{Kind: index.Clarification, Hash: ref})
		o.check("workflow_await_wait.go clarification including empty answer")
	}
	o.add(index.MessageEntry{Kind: index.Clarification, Hash: o.put([]byte("missing field answer"))})
	o.check("workflow_clarification.go appendClarificationAnswer")
	o.add(index.MessageEntry{Kind: index.Clarification, Hash: o.put(nil)})
	o.check("workflow_clarification.go empty answer")
	resume := &api.ResumeRequest{Messages: []*model.Message{nil, newTextAgentMessage(model.ConversationRoleUser, "resume")}}
	for _, message := range resume.Messages {
		o.add(index.MessageEntry{Kind: index.Message, Hash: o.putJSON(message)})
	}
	o.check("workflow_interrupts.go applyResumeMessages")
	payload := []byte(`{"approved":true}`)
	ref := o.put(payload)
	o.add(index.MessageEntry{Kind: index.TypedInput, Hash: ref, InputID: "typed"})
	o.check("typed inputs never enter Messages")
}

func TestIndexOracleFinalize(t *testing.T) {
	for _, reason := range []planner.TerminationReason{"", planner.TerminationReasonTimeBudget, planner.TerminationReasonToolCap, planner.TerminationReasonFailureCap, planner.TerminationReasonOutcomeUnknown} {
		for _, calls := range []bool{false, true} {
			t.Run(string(reason)+"/"+map[bool]string{false: "no calls", true: "pending calls"}[calls], func(t *testing.T) {
				o := newIndexOracle(t, []*model.Message{newTextAgentMessage(model.ConversationRoleUser, "request"), nil})
				if calls {
					batch := []planner.ToolRequest{{Name: "test.a", ToolCallID: "a"}, {Name: "test.b", ToolCallID: "b"}}
					o.assistant(nil, batch)
				}
				o.structural(index.FinalizeResults, nil)
				o.add(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeHint, ProtocolVersion: 5, Reason: string(reason)}})
				o.check("workflow_finalize.go synthetic results and versioned hint")
			})
		}
	}
}
