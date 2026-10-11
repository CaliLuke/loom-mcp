package agenttest_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessCloseReportsOpenLifecycle(t *testing.T) {
	entered := make(chan struct{}, 1)
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{block: true, entered: entered}))
	require.NoError(t, err)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, err := h.Run(t.Context(), "root", nil)
		if assert.NoError(t, err) {
			assert.ErrorIs(t, result.RunErr, context.Canceled)
		}
	}()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	err = h.Close()
	require.ErrorContains(t, err, "Wait has not returned")
	require.ErrorContains(t, err, "responder still alive")
	require.ErrorContains(t, err, "bus subscription still open")
	<-finished
	require.NoError(t, h.Close())
	_, err = h.Run(t.Context(), "root", nil)
	require.ErrorContains(t, err, "closed")
}

func TestHarnessModelOverrideAndApplicationClient(t *testing.T) {
	application, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("application")))
	require.NoError(t, err)
	override, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("override")))
	require.NoError(t, err)
	override.AnyOrder().WithContextLimits(model.ContextLimits{ContextWindow: 4096})
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("preserved", application.Client()); err != nil {
			return err
		}
		if err := rt.RegisterModel("model", application.Client()); err != nil {
			return err
		}
		return registerHarnessAgent("root", harnessPlanner{id: "model"})(ctx, rt)
	}
	h := agenttest.New(t, register, agenttest.ScriptedModelFrom("model", override))
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.Equal(t, "override", result.Output.Final.Parts[0].(model.TextPart).Text)
	assert.Equal(t, application.Client(), h.Model("preserved"))
	assert.Nil(t, h.Model("unknown"))
	assert.Nil(t, h.Script("preserved"))
	assert.Equal(t, 1, application.Remaining())
}

func TestHarnessDroppedAwaitTimeout(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "dropped", Question: "which?"}))}),
		agenttest.WithRunTimeout(50*time.Millisecond), agenttest.WithResponder(agenttest.Responder{agenttest.Clarify("yes")}),
		agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, in *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
			return &runtime.BeforeEventDecision{Drop: in.Event.Type() == hooks.AwaitClarification}, nil
		}})))
	require.NoError(t, err)
	_, err = h.Run(t.Context(), "root", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "BeforeEvent interceptor may have dropped an await")
	require.NoError(t, h.Close())
}
