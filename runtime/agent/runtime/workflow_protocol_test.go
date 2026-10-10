package runtime

import (
	"context"
	"fmt"
	"testing"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkflowProtocolVersion(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
	}{
		{"current", api.WorkflowProtocolVersion},
		{"missing", 0},
		{"future", api.WorkflowProtocolVersion + 1},
		{"previous", api.WorkflowProtocolVersion - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateWorkflowInput(&RunInput{AgentID: "svc.agent", WorkflowProtocolVersion: test.version})
			if test.version == api.WorkflowProtocolVersion {
				assert.NoError(t, err)
				return
			}
			var mismatch *api.WorkflowProtocolMismatchError
			require.ErrorAs(t, err, &mismatch)
			assert.Equal(t, api.WorkflowProtocolVersion, mismatch.Want)
			assert.Equal(t, test.version, mismatch.Got)
			require.ErrorIs(t, err, api.ErrWorkflowProtocolMismatch)
		})
	}
}

func TestStartStampsWorkflowProtocolAfterOptions(t *testing.T) {
	for _, version := range []int{0, api.WorkflowProtocolVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			eng := &stubEngine{}
			rt := New(WithEngine(eng))
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Workflow: engine.WorkflowDefinition{Name: "svc.workflow", TaskQueue: "q"}}
			_, err := rt.CreateSession(t.Context(), "session")
			require.NoError(t, err)
			handle, err := rt.MustClient(agent.Ident("svc.agent")).Start(t.Context(), "session", nil, func(input *RunInput) {
				input.WorkflowProtocolVersion = version
			})
			require.NoError(t, err)
			_, err = handle.Wait(t.Context())
			require.NoError(t, err)
			assert.Equal(t, api.WorkflowProtocolVersion, eng.last.Input.WorkflowProtocolVersion)
		})
	}
}

func TestChildInputsStampWorkflowProtocol(t *testing.T) {
	for _, kind := range []string{"agent-as-tool", "inline"} {
		t.Run(kind, func(t *testing.T) {
			nested := run.Context{RunID: "child", ParentRunID: "parent", SessionID: "session"}
			var input *RunInput
			if kind == "inline" {
				built := buildInlineAgentRunInput("child.agent", nested, nil)
				input = &built
			} else {
				wf := &testWorkflowContext{ctx: t.Context()}
				_, err := New().executeReservedAgentChild(wf, AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "q"}, nil, nested)
				require.NoError(t, err)
				require.Len(t, wf.childRequests, 1)
				input = wf.childRequests[0].Input
			}
			assert.Equal(t, api.WorkflowProtocolVersion, input.WorkflowProtocolVersion)
		})
	}
}

func TestExecuteWorkflowRejectsProtocolBeforeEffects(t *testing.T) {
	for _, version := range []int{0, api.WorkflowProtocolVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			beforeRuns := 0
			bus := &recordingHooks{}
			rt := New(WithHooks(bus), WithInterceptors(RuntimeInterceptorFuncs{
				BeforeRunFunc: func(context.Context, *BeforeRunInput) (*BeforeRunDecision, error) {
					beforeRuns++
					return nil, nil
				},
			}))
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", PlanActivityName: "plan"}
			wf := &testWorkflowContext{ctx: t.Context(), runtime: rt}
			out, err := rt.ExecuteWorkflow(wf, &RunInput{AgentID: "svc.agent", RunID: "run", WorkflowProtocolVersion: version})
			var mismatch *api.WorkflowProtocolMismatchError
			require.ErrorAs(t, err, &mismatch)
			require.ErrorIs(t, err, api.ErrWorkflowProtocolMismatch)
			assert.Nil(t, out)
			assert.Zero(t, beforeRuns)
			assert.Empty(t, bus.events)
			assert.Empty(t, wf.plannerCalls)
			assert.Empty(t, wf.lastHookCall.Name)
			assert.Empty(t, wf.lastToolCall.Name)
			assert.Empty(t, wf.childRequests)
		})
	}
}
