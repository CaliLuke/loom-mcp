package agenttest_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHarnessSession(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("first reply")), agenttest.Respond(agenttest.Text("second reply"))))
	result, err := h.RunSession(t.Context(), "root", agenttest.User("first"), agenttest.User("second"))
	require.NoError(t, err)
	require.Len(t, result.Runs, 2)
	require.Len(t, result.Trajectory, 2)
	assert.Equal(t, "agenttest-s1", result.SessionID)
	assert.Equal(t, result.SessionID, result.Runs[0].SessionID)
	assert.Equal(t, result.SessionID, result.Runs[1].SessionID)
	assert.Equal(t, []string{"agenttest-r1", "agenttest-r2"}, []string{result.Trajectory[0].RunID, result.Trajectory[1].RunID})
	require.Len(t, h.Script("model").Requests()[1].Messages, 1)
	assert.Equal(t, "second", h.Script("model").Requests()[1].Messages[0].Parts[0].(model.TextPart).Text)
}

func TestHarnessSessionStopsOnHarnessError(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"}))}))
	result, err := h.RunSession(t.Context(), "root", agenttest.User("first"), agenttest.User("second"))
	require.ErrorIs(t, err, agenttest.ErrUnansweredAwait)
	assert.Empty(t, result.Runs)
}

func TestHarnessTracing(t *testing.T) {
	t.Parallel()
	global := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		assert.NoError(t, provider.Shutdown(context.Background()))
	})
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.WithTracerProvider(provider), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
	result, err := h.Run(t.Context(), "root", agenttest.User("hello"))
	require.NoError(t, err)
	require.NotEmpty(t, result.TraceID)
	assert.Equal(t, result.TraceID, result.Trajectory.TraceID)
	names := make([]string, 0, len(recorder.Ended()))
	for _, span := range recorder.Ended() {
		assert.Equal(t, result.TraceID, span.SpanContext().TraceID().String())
		names = append(names, span.Name())
	}
	assert.Contains(t, names, "agenttest.run")
	assert.Contains(t, names, "planner.plan_start")
	assert.Contains(t, names, "model.complete")
	assert.Equal(t, global, otel.GetTracerProvider())
	encoded, err := result.Trajectory.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"trace_id":"`+result.TraceID+`"`)
	assert.Contains(t, result.Trajectory.Render(trajectory.RenderOptions{}), "Trace: "+result.TraceID)
}

func TestHarnessWithoutTracing(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	assert.Empty(t, result.TraceID)
	encoded, err := result.Trajectory.Marshal()
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "trace_id")
	assert.NotContains(t, result.Trajectory.Render(trajectory.RenderOptions{}), "Trace:")
}
