package runtime

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexOracleResultBatchAndReminders(t *testing.T) {
	o := newIndexOracle(t, nil)
	for _, name := range []tools.Ident{"test.small", "test.large", "test.failed", "test.missing"} {
		spec := newAnyJSONSpec(name, "test")
		spec.ResultReminder = "Remember " + string(name)
		o.rt.toolSpecs[name] = spec
	}
	calls := []planner.ToolRequest{
		{Name: "test.small", ToolCallID: "small"}, {Name: "test.large", ToolCallID: "large"},
		{Name: "test.failed", ToolCallID: "failed"}, {Name: "test.missing", ToolCallID: "missing"},
	}
	small := &planner.ToolResult{Name: "test.small", ToolCallID: "small", Result: map[string]any{"count": float64(7)}}
	large := &planner.ToolResult{Name: "test.large", ToolCallID: "large", Result: strings.Repeat("x", 100_000), Bounds: &agent.Bounds{Returned: 1, Truncated: true, RefinementHint: "narrow the query"}}
	failed := &planner.ToolResult{Name: "test.failed", ToolCallID: "failed", Error: planner.NewToolError("bad request"), RetryHint: &planner.RetryHint{Reason: planner.RetryReasonMissingFields, MissingFields: []string{"id"}, ClarifyingQuestion: "Which ID?"}}
	o.assistant(nil, calls)
	// Completion order and extra/missing values must not change dispatch order.
	o.results(small, large, failed)
	// Existing reminder producers are inputs to the index, not the renderer.
	var reminders []index.TextParameter
	for _, result := range []*planner.ToolResult{small, large, failed} {
		for _, text := range o.rt.toolResultReminders(result) {
			reminders = append(reminders, o.text(text))
		}
	}
	o.structural(index.Reminders, nil, reminders...)
	o.check("appendUserToolResults order, missing IDs, omission, retry/bounds reminders")
	require.Len(t, o.base.Messages, 3)
	assert.Len(t, o.base.Messages[1].Parts, 3)
	assert.Equal(t, model.ConversationRoleSystem, o.base.Messages[2].Role)
}

func TestIndexOracleBookkeepingHiding(t *testing.T) {
	for _, withText := range []bool{false, true} {
		t.Run(map[bool]string{false: "emptied assistant removed", true: "text retained"}[withText], func(t *testing.T) {
			o := newIndexOracle(t, nil)
			spec := bookkeepingSpec("control.progress", false)
			spec.ResultReminder = "must not render"
			o.rt.toolSpecs[spec.Name] = spec
			o.rt.toolSpecs["test.lookup"] = newAnyJSONSpec("test.lookup", "test")
			first := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-1"}
			second := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-2"}
			var messages []*model.Message
			if withText {
				messages = []*model.Message{newTextAgentMessage(model.ConversationRoleAssistant, "keep this")}
			}
			o.assistant(messages, []planner.ToolRequest{first})
			o.assistant(nil, []planner.ToolRequest{second})
			committed, err := index.NewSegment("", "", o.entries)
			require.NoError(t, err)
			before, head, err := index.EncodeSegment(committed)
			require.NoError(t, err)
			for _, call := range []planner.ToolRequest{first, second} {
				result := &planner.ToolResult{Name: spec.Name, ToolCallID: call.ToolCallID, Result: true}
				o.structural(index.HideBookkeeping, []string{call.ToolCallID})
				o.check("backward hiding across assistant messages")
				o.results(result)
				o.check("bookkeeping-only batch skips user message and reminder")
			}
			after, sameHead, err := index.EncodeSegment(committed)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.Equal(t, head, sameHead)
			pending, err := index.NewSegment(head, committed.Entries[len(committed.Entries)-1].Chain, o.entries[len(committed.Entries):])
			require.NoError(t, err)
			assert.Equal(t, head, *pending.Prev)
		})
	}
}

func TestIndexOracleMixedBookkeepingBatch(t *testing.T) {
	o := newIndexOracle(t, nil)
	o.rt.toolSpecs["control.progress"] = bookkeepingSpec("control.progress", false)
	o.rt.toolSpecs["test.lookup"] = newAnyJSONSpec("test.lookup", "test")
	calls := []planner.ToolRequest{{Name: "control.progress", ToolCallID: "hidden"}, {Name: "test.lookup", ToolCallID: "visible"}, {Name: "control.progress", ToolCallID: "failed"}}
	results := []*planner.ToolResult{{Name: "control.progress", ToolCallID: "hidden", Result: true}, {Name: "test.lookup", ToolCallID: "visible", Result: "ok"}, {Name: "control.progress", ToolCallID: "failed", Error: planner.NewToolError("failed")}}
	o.assistant(nil, calls)
	o.structural(index.HideBookkeeping, []string{"hidden"})
	o.results(results...)
	o.check("mixed batch retains domain and failed bookkeeping results")
}

func TestIndexOracleStructuralResults(t *testing.T) {
	for _, tc := range []struct {
		kind index.StructuralKind
		text string
	}{
		{index.UnknownTool, "bad.\"tool"},
		{index.PolicyDenial, "not permitted"},
		{index.SynthesizedError, "execution failed"},
		{index.SynthesizedError, ""},
		{index.SynthesizedError, strings.Repeat("failure", 3000)},
		{index.Cancellation, ""},
		{index.ConfirmationDenial, `{"summary":"declined"}`},
	} {
		t.Run(string(tc.kind)+"/"+tc.text[:min(12, len(tc.text))], func(t *testing.T) {
			o := newIndexOracle(t, nil)
			call := planner.ToolRequest{Name: "test.tool", ToolCallID: "call"}
			o.rt.toolSpecs[call.Name] = newAnyJSONSpec(call.Name, "test")
			result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}
			s := &index.StructuralEntry{Kind: tc.kind, ProtocolVersion: 5, Text: []index.TextParameter{o.text(tc.text)}}
			switch tc.kind {
			case index.CarriedResult:
				t.Fatal("carried results are tested by continuation scenarios")
			case index.UnknownTool:
				call.Name = tools.Ident(tc.text)
				exec := &toolBatchExec{r: o.rt}
				var err error
				result, err = exec.synthesizeUnknownToolResult(o.wf.Context(), call, 0)
				require.NoError(t, err)
			case index.Cancellation:
				s.Text = nil
				exec := &toolBatchExec{r: o.rt}
				var err error
				result, err = exec.newCanceledToolResult(o.wf.Context(), call, "canceled: time budget reached")
				require.NoError(t, err)
			case index.ConfirmationDenial:
				result.Result = map[string]any{"summary": "declined"}
			case index.PolicyDenial, index.SynthesizedError:
				result.Error = planner.NewToolError(tc.text)
				s.Text = []index.TextParameter{o.text(result.Error.Error())}
			case index.FinalizeResults, index.FinalizeHint, index.Reminders, index.HideBookkeeping:
				t.Fatalf("%s is not a result template", tc.kind)
			}
			o.assistant(nil, []planner.ToolRequest{call})
			o.add(index.MessageEntry{Kind: index.Structural, Batch: index.MessageID(len(o.entries) + 1), ToolUseIDs: []string{call.ToolCallID}, Structural: s})
			resultReminders := o.rt.toolResultReminders(result)
			reminders := make([]index.TextParameter, 0, len(resultReminders))
			for _, text := range resultReminders {
				reminders = append(reminders, o.text(text))
			}
			if len(reminders) > 0 {
				o.structural(index.Reminders, nil, reminders...)
			}
			o.check("structural template agrees with current tool-result projection")
		})
	}
}
