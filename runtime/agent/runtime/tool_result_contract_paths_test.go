package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestMissingFieldsContractAcrossResultPaths(t *testing.T) {
	for _, question := range []string{"", " \t\n"} {
		for _, path := range []string{"provided", "child", "inline"} {
			t.Run(fmt.Sprintf("%s/question=%q", path, question), func(t *testing.T) {
				rt := New()
				spec := newAnyJSONSpec("test.lookup", "test")
				hint := &planner.RetryHint{Reason: planner.RetryReasonMissingFields, ClarifyingQuestion: question}
				reg := ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
					return Executed(&planner.ToolResult{Error: planner.NewToolError("missing topic"), RetryHint: hint}), nil
				}}
				require.NoError(t, rt.RegisterToolset(reg))
				call := planner.ToolRequest{Name: spec.Name, ToolCallID: "call", RootRunID: "root", RunID: "root"}
				exec := &toolBatchExec{r: rt}
				wfCtx := &testWorkflowContext{ctx: t.Context()}
				var err error
				switch path {
				case "provided":
					_, _, err = rt.decodeProvidedToolResults(t.Context(), []planner.ToolRequest{call}, map[string]*api.ProvidedToolResult{
						"call": {Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("missing topic"), RetryHint: hint},
					})
				case "child":
					ready := make(chan struct{})
					close(ready)
					_, err = exec.collectChildResult(wfCtx, t.Context(), agentChildFutureInfo{
						call: call, cfg: &AgentToolConfig{AgentID: "child.agent"}, nestedRun: run.Context{RunID: "child"},
						handle: &controlledChildHandle{ready: ready, out: &RunOutput{FinalToolResult: &api.ToolEvent{Error: planner.NewToolError("missing topic"), RetryHint: hint}}},
					})
				case "inline":
					err = exec.dispatchInlineToolCall(wfCtx, newToolCallBatch(nil), call, reg)
				}
				assertToolResultContractError(t, err)
			})
		}
	}
}

func TestProvidedResultShapeContractIsNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name string
		item *api.ProvidedToolResult
	}{
		{name: "nil result"},
		{name: "result and error", item: &api.ProvidedToolResult{Result: rawjson.Message(`{"ok":true}`), Error: planner.NewToolError("failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			spec := newAnyJSONSpec("test.lookup", "test")
			_, _, err := rt.decodeProvidedToolResult(t.Context(), spec, planner.ToolRequest{Name: spec.Name, ToolCallID: "call"}, tc.item)
			assertToolResultContractError(t, err)
		})
	}
}

func TestSynthesizedResultContractIsNonRetryable(t *testing.T) {
	for _, path := range []string{"expired", "timeout", "execution failure", "request error"} {
		t.Run(path, func(t *testing.T) {
			rt := New()
			spec := newAnyJSONSpec("test.lookup", "test")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
				Name: "test", Specs: []tools.ToolSpec{spec},
				Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
					panic("synthesized results must not execute the tool")
				},
				ResultMaterializer: func(_ context.Context, _ ToolCallMeta, _ *planner.ToolRequest, result *planner.ToolResult) error {
					result.Bounds = &agent.Bounds{}
					return nil
				},
			}))
			exec := &toolBatchExec{r: rt}
			call := planner.ToolRequest{Name: spec.Name, ToolCallID: "call"}
			var err error
			switch path {
			case "expired":
				_, err = exec.newCanceledToolResult(t.Context(), call, "expired")
			case "timeout":
				_, err = exec.synthesizeTimeoutToolResult(&testWorkflowContext{ctx: t.Context()}, call, time.Time{}, "timeout")
			case "execution failure":
				_, err = exec.synthesizeToolError(t.Context(), call, collectedExecutionFailure{text: "failed"}, "tool failed", 0)
			case "request error":
				call.Error = planner.NewToolError("invalid request")
				err = exec.dispatchToolRequestError(t.Context(), newToolCallBatch(nil), call)
			}
			assertToolResultContractError(t, err)
		})
	}
}
