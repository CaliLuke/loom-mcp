package agenttest_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScenarioSingleTurnFixture(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "item overrides"}[override], func(t *testing.T) {
			h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})), agenttest.Respond(agenttest.Text("ready"))))
			h.Passthrough(projected.ProjectedStatusToolTool)
			item := eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{Exact: true, Tools: []expect.ExpectedTool{{Name: projected.ProjectedStatusTool}}}}
			task, reference := "default task", "default reference"
			if override {
				item.Task = "item task"
				item.Reference = "item reference"
				task = item.Task
				reference = item.Reference
			}
			s := eval.Scenario{ID: "status", Agent: "assistant.assistant_runtime", Task: "default task", Reference: "default reference", Items: eval.Items(item), Expect: []expect.Expectation{expect.Completed()}}
			s.Assert = func(ctx context.Context, a *eval.Attempt) (eval.Assertions, error) {
				assert.Equal(t, task, a.Item.Task)
				assert.Equal(t, reference, a.Item.Reference)
				expect.Require(t, a.Subject(), expect.Call(projected.ProjectedStatusToolTool, nil, nil))
				require.NotNil(t, a.Final())
				assert.Equal(t, "ready", a.Final().Parts[0].(model.TextPart).Text)
				return eval.Assertions{Checks: []expect.Check{{Name: "final", Passed: true}}}, nil
			}
			out, err := eval.Execute(t.Context(), h, s, item)
			require.NoError(t, err)
			require.NoError(t, out.Err)
			assert.Equal(t, task, out.Task)
			assert.Equal(t, reference, out.Reference)
			names := make([]string, len(out.Checks))
			for n, c := range out.Checks {
				names[n] = c.Name
				assert.True(t, c.Passed, c.Diagnostic)
			}
			assert.Equal(t, []string{"runs_succeeded", "completed", "expected_tools", "final"}, names)
			assert.Equal(t, []expect.Score{{Name: "model_calls", Value: 2}, {Name: "tool_calls", Value: 1}, {Name: "failed_tool_calls"}, {Name: "repeated_tool_calls"}, {Name: "tool_correctness", Value: 1}}, out.Scores)
		})
	}
}

func TestScenarioMultiTurnFixture(t *testing.T) {
	h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("first")), agenttest.Respond(agenttest.Text("second"))))
	item := eval.Item{ID: "two", Turns: []string{"hello", "again"}, ExpectedTools: &expect.ExpectedTools{Exact: true}}
	s := eval.Scenario{ID: "conversation", Agent: "assistant.assistant_runtime", Items: eval.Items(item)}
	out, err := eval.Execute(t.Context(), h, s, item)
	require.NoError(t, err)
	require.NoError(t, out.Err)
	require.Len(t, out.Session.Trajectory, 2)
	assert.Equal(t, out.Session.SessionID, out.Session.Trajectory[0].SessionID)
	assert.Equal(t, out.Session.SessionID, out.Session.Trajectory[1].SessionID)
}

func TestScenarioUnstubbedFixture(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerScenarioFixture, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})), agenttest.Respond(agenttest.Text("ready"))))
	require.NoError(t, err)
	t.Cleanup(func() {
		var stub *agenttest.StubError
		assert.ErrorAs(t, h.Close(), &stub)
	})
	called := false
	item := eval.Item{ID: "one", Input: "status?"}
	s := eval.Scenario{ID: "status", Agent: "assistant.assistant_runtime", Items: eval.Items(item), Assert: func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		called = true
		return eval.Assertions{}, nil
	}}
	out, err := eval.Execute(t.Context(), h, s, item)
	require.NoError(t, err)
	var stub *agenttest.StubError
	require.ErrorAs(t, out.Err, &stub)
	assert.NotNil(t, out.Session)
	assert.Empty(t, out.Checks)
	assert.Empty(t, out.Scores)
	assert.False(t, called)
}

func TestScenarioAgentFailureFixture(t *testing.T) {
	h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture", agenttest.Fail(errors.New("model broke"))))
	item := eval.Item{ID: "one", Input: "hi", ExpectedTools: &expect.ExpectedTools{}}
	s := eval.Scenario{ID: "failure", Agent: "assistant.assistant_runtime", Items: eval.Items(item)}
	out, err := eval.Execute(t.Context(), h, s, item)
	require.NoError(t, err)
	require.NoError(t, out.Err)
	assert.False(t, out.Checks[0].Passed)
	assert.Contains(t, out.Checks[0].Diagnostic, out.Session.Runs[0].RunID)
	assert.Contains(t, out.Checks[0].Diagnostic, "model broke")
}

func TestScenarioConcurrentFixture(t *testing.T) {
	h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("one")).When(agenttest.LastUserContains("one")), agenttest.Respond(agenttest.Text("two")).When(agenttest.LastUserContains("two"))))
	h.Script("fixture").AnyOrder()
	items := []eval.Item{{ID: "one", Input: "one", ExpectedTools: &expect.ExpectedTools{}}, {ID: "two", Input: "two", ExpectedTools: &expect.ExpectedTools{}}}
	s := eval.Scenario{ID: "parallel", Agent: "assistant.assistant_runtime", Items: eval.Items(items...)}
	var wg sync.WaitGroup
	outcomes := make([]*eval.Outcome, 2)
	errs := make([]error, 2)
	for n, item := range items {
		wg.Go(func() {
			outcomes[n], errs[n] = eval.Execute(t.Context(), h, s, item)
		})
	}
	wg.Wait()
	for n, out := range outcomes {
		require.NoError(t, errs[n])
		require.NotNil(t, out)
		require.NoError(t, out.Err)
		assert.True(t, out.Checks[0].Passed)
	}
	assert.NotEqual(t, outcomes[0].Session.SessionID, outcomes[1].Session.SessionID)
}

func TestScenarioUnverifiedEventsFixture(t *testing.T) {
	h := agenttest.New(t, registerScenarioFixture,
		agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"))),
		agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{
			BeforeEventFunc: func(context.Context, *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
				return nil, nil
			},
		})))
	item := eval.Item{ID: "one", Input: "hello", ExpectedTools: &expect.ExpectedTools{}}
	s := eval.Scenario{ID: "unverified", Agent: "assistant.assistant_runtime", Items: eval.Items(item)}
	out, err := eval.Execute(t.Context(), h, s, item)
	require.NoError(t, err)
	require.NoError(t, out.Err)
	assert.False(t, out.Session.Trajectory[0].StatusVerified)
	assert.True(t, out.Checks[0].Passed)
}

func registerScenarioFixture(ctx context.Context, rt *runtime.Runtime) error {
	executor := runtime.ToolCallExecutorFunc(func(_ context.Context, _ *runtime.ToolCallMeta, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: &projected.ProjectedStatusToolResult{Status: "ready"}}), nil
	})
	if err := generated.RegisterUsedToolsets(ctx, rt, generated.WithProjectedExecutor(executor)); err != nil {
		return err
	}
	return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: fixturePlanner{}})
}
