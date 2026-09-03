package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/require"
)

const routeTimingRunTimeout = 32*time.Minute + 30*time.Second

type recordingChildWorkflowContext struct {
	*routeWorkflowContext
	request engine.ChildWorkflowRequest
}

func (w *recordingChildWorkflowContext) StartChildWorkflow(_ context.Context, req engine.ChildWorkflowRequest) (engine.ChildWorkflowHandle, error) {
	w.request = req
	return &testChildHandle{request: req, wfCtx: w}, nil
}

func workerTimingRoute() AgentRoute {
	return AgentRoute{
		ID:                    "service.agent",
		WorkflowName:          "service.workflow",
		DefaultTaskQueue:      "svc.queue",
		TimeBudget:            30 * time.Minute,
		FinalizerGrace:        10 * time.Second,
		ResumeActivityTimeout: 2 * time.Minute,
	}
}

func TestRouteClientUsesWorkerTimingForEngineTimeout(t *testing.T) {
	eng := &stubEngine{}
	rt := &Runtime{
		Engine:       eng,
		SessionStore: inmem.New(),
		logger:       telemetry.NoopLogger{},
		metrics:      telemetry.NoopMetrics{},
		tracer:       telemetry.NoopTracer{},
	}
	client := rt.MustClientFor(workerTimingRoute())
	_, err := rt.CreateSession(context.Background(), "sess-1")
	require.NoError(t, err)

	_, err = client.Start(context.Background(), "sess-1", nil)
	require.NoError(t, err)
	require.Equal(t, routeTimingRunTimeout, eng.last.RunTimeout)
}

func TestRouteOneShotClientUsesWorkerTimingForEngineTimeout(t *testing.T) {
	eng := &stubEngine{}
	rt := &Runtime{
		Engine:  eng,
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}
	client := rt.MustClientFor(workerTimingRoute())

	_, err := client.OneShotRun(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, routeTimingRunTimeout, eng.last.RunTimeout)
}

func TestRouteChildWorkflowsUseWorkerTimingForEngineTimeout(t *testing.T) {
	route := workerTimingRoute()
	wfCtx := &recordingChildWorkflowContext{
		routeWorkflowContext: &routeWorkflowContext{ctx: context.Background()},
	}

	_, err := (&Runtime{}).ExecuteAgentChildWithRoute(wfCtx, route, nil, run.Context{RunID: "child-run"})
	require.NoError(t, err)
	require.Equal(t, routeTimingRunTimeout, wfCtx.request.RunTimeout)

	input := &RunInput{AgentID: route.ID, RunID: "inline-child-run"}
	_, err = startInlineAgentChildWorkflow(wfCtx, context.Background(), route, input)
	require.NoError(t, err)
	require.Equal(t, routeTimingRunTimeout, wfCtx.request.RunTimeout)
}
