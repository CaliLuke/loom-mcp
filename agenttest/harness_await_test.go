package agenttest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessAwaits(t *testing.T) {
	for _, tc := range []struct {
		name string
		item planner.AwaitItem
		rule agenttest.AwaitRule
		kind hooks.EventType
	}{
		{"clarify", planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"}), agenttest.Clarify("yes"), hooks.AwaitClarification},
		{"typed", planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "await", Title: "input", Schema: rawjson.Message(`{"type":"object"}`)}), agenttest.TypedInput(rawjson.Message(`{"value":"yes"}`)), hooks.AwaitTypedInput},
		{"questions", planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "await", ToolName: "echo.echo", ToolCallID: "external", Payload: rawjson.Message(`{}`), Questions: []planner.AwaitQuestion{{ID: "question", Prompt: "pick", Options: []planner.AwaitQuestionOption{{ID: "yes", Label: "Yes"}}}}}), agenttest.Answer("question", "yes"), hooks.AwaitQuestions},
		{"external", planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "await", Items: []planner.AwaitToolItem{{Name: "echo.echo", ToolCallID: "external", Payload: rawjson.Message(`{}`)}}}), agenttest.ExternalToolsFunc(func(event *hooks.AwaitExternalToolsEvent) ([]*api.ProvidedToolResult, error) {
			return []*api.ProvidedToolResult{{Name: event.Items[0].ToolName, ToolCallID: event.Items[0].ToolCallID, Result: rawjson.Message(`{"value":"yes"}`)}}, nil
		}), hooks.AwaitExternalTools},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", await: planner.NewAwait(tc.item), specs: []tools.ToolSpec{echoSpec()}}), agenttest.WithResponder(agenttest.Responder{tc.rule}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
			result, err := h.Run(t.Context(), "root", nil)
			require.NoError(t, err)
			require.NoError(t, result.RunErr)
			require.Len(t, result.Awaits, 1)
			assert.Equal(t, tc.kind, result.Awaits[0].Kind)
			assert.Equal(t, "await", result.Awaits[0].ID)
			assert.NotNil(t, result.Awaits[0].Answer)
			assert.Contains(t, result.Trajectory.Render(trajectory.RenderOptions{}), "done")
		})
	}
}

func TestHarnessConfirmation(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(map[bool]string{true: "approve", false: "deny"}[approved], func(t *testing.T) {
			rule := agenttest.Deny(tools.Ident("echo.echo"))
			if approved {
				rule = agenttest.Approve(tools.Ident("echo.echo"))
			}
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
				agenttest.WithRuntimeOptions(runtime.WithToolConfirmation(&runtime.ToolConfirmationConfig{Confirm: map[tools.Ident]*runtime.ToolConfirmation{"echo.echo": {Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
					return "execute?", nil
				}, DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) {
					return map[string]any{"value": "denied"}, nil
				}}}})),
				// This works around a known runtime defect: confirmation answers resume
				// planning twice (workflow_await_queue.go:276 and workflow_turn.go:120).
				// The defect is tracked as a follow-up; this is not intended behavior.
				agenttest.WithResponder(agenttest.Responder{rule}), agenttest.ScriptedModel("model",
					agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))),
					agenttest.Respond(agenttest.Text("first confirmation resume")),
					agenttest.Respond(agenttest.Text("second confirmation resume"))))
			h.Passthrough(echoTool())
			result, err := h.Run(t.Context(), "root", nil)
			require.NoError(t, err)
			require.NoError(t, result.RunErr)
			require.Len(t, result.Awaits, 1)
			assert.Equal(t, hooks.AwaitConfirmation, result.Awaits[0].Kind)
			assert.Equal(t, approved, result.Awaits[0].Answer.(*api.ConfirmationDecision).Approved)
			assert.Contains(t, result.Trajectory.Render(trajectory.RenderOptions{}), map[bool]string{true: `"Approved":true`, false: `"Approved":false`}[approved])
		})
	}
}

func TestHarnessUnansweredAwait(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "missing", Question: "which?"}))}), agenttest.WithResponder(agenttest.Responder{agenttest.Deny(tools.Ident("different.tool"))}))
	require.NoError(t, err)
	result, err := h.Run(t.Context(), "root", nil)
	assert.Nil(t, result)
	require.ErrorIs(t, err, agenttest.ErrUnansweredAwait)
	require.ErrorContains(t, err, "await_clarification")
	require.ErrorContains(t, err, "missing")
	require.ErrorContains(t, err, "which?")
	require.NoError(t, h.Close())
	runs, listErr := h.Runtime().SessionStore.ListRunsBySession(t.Context(), "agenttest-s1", nil)
	require.NoError(t, listErr)
	require.Len(t, runs, 1)
	assert.Equal(t, "canceled", string(runs[0].Status))
}

func TestHarnessResponderFirstMatch(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"}))}),
		agenttest.WithResponder(agenttest.Responder{agenttest.ClarifyFunc(func(*hooks.AwaitClarificationEvent) (string, error) {
			return "first", nil
		}), agenttest.Clarify("second")}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.Len(t, result.Awaits, 1)
	assert.Equal(t, "first", result.Awaits[0].Answer.(*api.ClarificationAnswer).Answer)
}

func TestHarnessResponderFailure(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"}))}),
		agenttest.WithResponder(agenttest.Responder{agenttest.ClarifyFunc(func(*hooks.AwaitClarificationEvent) (string, error) {
			return "", errors.New("responder failed")
		})}))
	require.NoError(t, err)
	_, err = h.Run(t.Context(), "root", nil)
	require.ErrorContains(t, err, "responder failed")
	require.NoError(t, h.Close())
}

func TestHarnessResponderCallbacks(t *testing.T) {
	// Every functional rule reaches its matching signal path, including questions
	// whose result payload is application-owned.
	rules := agenttest.Responder{
		agenttest.TypedInputFunc(func(*hooks.AwaitTypedInputEvent) (rawjson.Message, error) {
			return rawjson.Message(`{}`), nil
		}),
		agenttest.QuestionsFunc(func(*hooks.AwaitQuestionsEvent) (rawjson.Message, error) {
			return rawjson.Message(`{"answers":[]}`), nil
		}),
	}
	items := planner.NewAwait(
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "typed", Schema: rawjson.Message(`{"type":"object"}`)}),
		planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "questions", ToolName: "echo.echo", ToolCallID: "questions", Payload: rawjson.Message(`{}`)}))
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", await: items, specs: []tools.ToolSpec{echoSpec()}}), agenttest.WithResponder(rules), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
	result, err := h.Run(context.Background(), "root", nil)
	require.NoError(t, err)
	assert.NoError(t, result.RunErr)
	assert.Len(t, result.Awaits, 2)
}
