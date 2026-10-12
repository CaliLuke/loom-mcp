package runtime

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Enumerate production entry constructors so a new writer cannot silently
// escape the measurement matrix. Empty error-return literals are not writers.
func TestEntryWriterInventory(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var writers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			writes := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok {
					return true
				}
				selector, ok := literal.Type.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "MessageEntry" {
					return true
				}
				for _, field := range literal.Elts {
					pair, ok := field.(*ast.KeyValueExpr)
					if ok {
						key, ok := pair.Key.(*ast.Ident)
						if ok && key.Name == "Kind" {
							writes = true
						}
					}
				}
				return true
			})
			if writes {
				writers = append(writers, name+":"+fn.Name.Name)
			}
		}
	}
	assert.ElementsMatch(t, []string{
		"record_content.go:recordContentItems",
		"continuation_seal.go:storeCarried",
		"continuation_seal.go:storeCarriedUsers",
		"continuation_seed.go:appendContinuationInput",
		"workflow_index.go:recordAssistant",
		"workflow_index.go:structural",
		"workflow_index_results.go:structuralIndexEntry",
		"workflow_index_results.go:resultIndexEntry",
		"workflow_interrupts.go:applyResumeMessages",
		"workflow_clarification.go:appendClarificationAnswer",
		"workflow_clarification.go:appendClarificationExchange",
		"workflow_finalize.go:buildFinalizePlanRequest",
		"workflow_await_wait.go:waitAwaitTypedInput",
	}, writers)
}

func TestResumeClarificationAndTypedEntrySizes(t *testing.T) {
	base := &workflowPlanInput{}
	attempt := 2
	require.NoError(t, applyResumeMessages(base, &attempt, &api.ResumeRequest{MessageRefs: []runstore.Hash{"message"}, MessageSizes: []contextmeasure.MessageSize{{Bytes: 9, NonText: 1500, Role: model.ConversationRoleUser}}}))
	assert.Equal(t, 9, base.Entries[0].Bytes)
	assert.Equal(t, 1500, base.Entries[0].NonText)
	assert.Equal(t, model.ConversationRoleUser, base.Entries[0].Role)
	require.NoError(t, appendClarificationAnswer(base, "answer", new(6)))
	assert.Equal(t, 6, base.Entries[1].Bytes)
	assert.Equal(t, model.ConversationRoleUser, base.Entries[1].Role)
	// Both missing-field clarification and explicit await use the same writer.
	rt := New()
	base.AgentID = "agent"
	base.RunContext.RootRunID = "root"
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt}
	wf.ensureSignals()
	wf.clarifyCh <- &api.ClarificationAnswer{ID: "question", AnswerRef: "other", AnswerBytes: new(5)}
	_, err := waitAwaitClarification(wf.Context(), interrupt.NewController(wf), base, 0, &planner.AwaitClarification{ID: "question", Question: "which?"}, "")
	require.NoError(t, err)
	assert.Equal(t, 6, base.Entries[2].Bytes)
	assert.Equal(t, model.ConversationRoleAssistant, base.Entries[2].Role)
	assert.Equal(t, 5, base.Entries[3].Bytes)
	assert.Equal(t, model.ConversationRoleUser, base.Entries[3].Role)
	typedBase := testWorkflowInput(t, rt, &planner.PlanInput{})
	typedWF := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt}
	typedWF.ensureSignals()
	typedWF.typedInputCh <- testTypedSignal(t, rt, typedBase.RunContext.RootRunID, &api.TypedInputAnswer{ID: "approval", Payload: rawjson.Message(`{"approved":true}`)})
	_, err = waitAwaitTypedInput(typedWF.Context(), interrupt.NewController(typedWF), &RunInput{AgentID: typedBase.AgentID}, typedBase, 0, &planner.AwaitTypedInput{ID: "approval", Schema: rawjson.Message(`{"type":"object"}`)})
	require.NoError(t, err)
	require.Len(t, typedBase.Entries, 1)
	assert.Zero(t, typedBase.Entries[0].Bytes)
	assert.Zero(t, typedBase.Entries[0].NonText)
	assert.Empty(t, typedBase.Entries[0].Role)
}

func TestCallOnlyAssistantSize(t *testing.T) {
	base := &workflowPlanInput{CallRefs: map[string]runstore.Hash{"id": "call"}}
	require.NoError(t, base.recordAssistant("", []planner.ToolRequest{{Name: "tool", ToolCallID: "id", Payload: rawjson.Message(`{"x":1}`)}}))
	assert.Equal(t, 13, base.Entries[0].Bytes)
	assert.Equal(t, model.ConversationRoleAssistant, base.Entries[0].Role)
	assert.Equal(t, []string{"id"}, base.Entries[0].ToolUseIDs)
}

func TestResultWriterSizes(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		resultBytes, expected int
		preview               string
		fail, denial          bool
	}{
		{name: "whole", resultBytes: 11, expected: 11},
		{name: "omitted", resultBytes: 100000, preview: "preview", expected: 7},
		{name: "error", fail: true, expected: 9},
		{name: "denial", resultBytes: 11, expected: 11, denial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			rt.toolSpecs["test.tool"] = newAnyJSONSpec("test.tool", "test")
			base := &workflowPlanInput{CallRefs: map[string]runstore.Hash{"id": "call"}, content: &workflowContentBatch{}, compaction: &ContextCompaction{KeepResultTools: []tools.Ident{"test.tool"}}}
			result := &planner.ToolResult{Name: "test.tool", ToolCallID: "id", Refs: tools.ResultRefs{Result: "result"}, ResultBytes: tc.resultBytes, Preview: tc.preview}
			if tc.fail {
				result.Error = planner.NewToolError("error")
				result.Refs.Result = ""
			}
			ctx := context.WithValue(t.Context(), workflowContentKey{}, base.content)
			entry, err := rt.resultIndexEntry(ctx, base, planner.ToolRequest{Name: "test.tool", ToolCallID: "id"}, result, 1, tc.denial)
			require.NoError(t, err)
			assert.True(t, entry.Keep)
			assert.Equal(t, tc.expected, entry.Bytes)
			assert.Zero(t, entry.NonText)
			assert.Equal(t, model.ConversationRoleUser, entry.Role)
			if tc.name == "omitted" {
				assert.True(t, entry.OmitsResultContent())
				assert.Equal(t, 100000, entry.ContentBytes())
			}
		})
	}
}

func TestStructuralWriterSizes(t *testing.T) {
	for _, tc := range []struct {
		kind     index.StructuralKind
		text     string
		expected int
		role     model.ConversationRole
	}{
		{kind: index.UnknownTool, text: "bad", expected: 24, role: model.ConversationRoleUser},
		{kind: index.PolicyDenial, text: "denied", expected: 10, role: model.ConversationRoleUser},
		{kind: index.ConfirmationDenial, text: `{"ok":false}`, expected: 14, role: model.ConversationRoleUser},
		{kind: index.SynthesizedError, text: "error", expected: 9, role: model.ConversationRoleUser},
		{kind: index.Cancellation, expected: 33, role: model.ConversationRoleUser},
		{kind: index.Reminders, text: "remember", expected: 43, role: model.ConversationRoleSystem},
		{kind: index.HideBookkeeping},
		{kind: index.FinalizeHint, role: model.ConversationRoleSystem},
		{kind: index.FinalizeResults, expected: 65, role: model.ConversationRoleUser},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			base := &workflowPlanInput{content: &workflowContentBatch{}}
			entry := structuralIndexEntry(tc.kind, []string{"id"})
			if tc.text != "" {
				entry.Structural.Text = []index.TextParameter{{Text: tc.text}}
			}
			entry.Batch = 1
			if tc.kind == index.FinalizeResults {
				base.CallRefs = map[string]runstore.Hash{"id": "call"}
				require.NoError(t, base.recordAssistant("", []planner.ToolRequest{{Name: "tool", ToolCallID: "id"}}))
			}
			require.NoError(t, base.appendEntry(entry))
			got := base.Entries[len(base.Entries)-1]
			if tc.kind == index.FinalizeHint {
				assert.Greater(t, got.Bytes, 100)
			} else {
				assert.Equal(t, tc.expected, got.Bytes)
			}
			assert.Zero(t, got.NonText)
			assert.Equal(t, tc.role, got.Role)
		})
	}
}
