package agenttest_test

import (
	"context"
	"encoding/json/jsontext"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolFirstCapturePlanner struct {
	capturePlanner
}

func TestCaptureRegistrationRetainedToolClient(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter bool
	}{
		{name: "executor"},
		{name: "payload_adapter", adapter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := &registrationCaptureClient{}
			var probes atomic.Int32
			h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
				if err := rt.RegisterModel("model", live); err != nil {
					return err
				}
				held, ok := rt.ModelClient("model")
				require.True(t, ok)
				probe := func(ctx context.Context) {
					probes.Add(1)
					_, err := held.Complete(ctx, &model.Request{Model: "provider", Messages: agenttest.User("tool side call")})
					assert.ErrorIs(t, err, agenttest.ErrCaptureSentinel)
				}
				toolset := runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{echoSpec()}, Execute: func(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
					if !tc.adapter {
						probe(ctx)
					}
					return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"value": "done"}}), nil
				}}
				if tc.adapter {
					toolset.PayloadAdapter = func(ctx context.Context, _ runtime.ToolCallMeta, _ tools.Ident, raw jsontext.Value) (jsontext.Value, error) {
						probe(ctx)
						return raw, nil
					}
				}
				if err := rt.RegisterToolset(toolset); err != nil {
					return err
				}
				return rt.RegisterAgent(ctx, runtime.AgentRegistration{
					ID: "root", Planner: toolFirstCapturePlanner{}, Specs: []tools.ToolSpec{echoSpec()},
					Workflow:         engine.WorkflowDefinition{Name: "tool_first.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
					PlanActivityName: "tool_first.plan", ResumeActivityName: "tool_first.resume", ExecuteToolActivity: "tool_first.tool",
				})
			})
			h.Passthrough(echoTool())

			request, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("tool first"))
			assert.EqualValues(t, 1, probes.Load(), "the retained-client callback must run")
			assert.Zero(t, live.calls.Load(), "retained tool clients must not reach the provider")
			require.NoError(t, err)
			assert.Equal(t, "model", request.ModelID())
			require.NotNil(t, request.Source())
			assert.Equal(t, 1, request.Source().Seq)
			calls := h.ToolCalls()
			require.Len(t, calls, 1)
			assert.True(t, calls[0].Passthrough)
		})
	}
}

func (p toolFirstCapturePlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "echo.echo", ToolCallID: "probe", Payload: []byte(`{"value":"input"}`)}}}, nil
}
