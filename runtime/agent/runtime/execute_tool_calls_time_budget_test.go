package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestExecuteToolCalls_CancelsInFlightToolsWhenTimeBudgetReached(t *testing.T) {
	recorder := &recordingHooks{}
	rt := &Runtime{
		Bus:           recorder,
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
		toolsets: map[string]ToolsetRegistration{
			"svc.tools": {},
		},
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			tools.Ident("svc.tools.slow"): newAnyJSONSpec("svc.tools.slow", "svc.tools"),
		},
	}

	fut := &controlledToolFuture{ready: make(chan struct{})}
	wfCtx := &testWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		toolFutures: map[string]*controlledToolFuture{"call-slow": fut},
	}

	runCtx := &run.Context{RunID: "run-1", SessionID: "sess-1", TurnID: "turn-1"}
	calls := []planner.ToolRequest{
		{Name: tools.Ident("svc.tools.slow"), RunID: runCtx.RunID, SessionID: runCtx.SessionID, TurnID: runCtx.TurnID, ToolCallID: "call-slow"},
	}

	finishBy := wfCtx.Now().Add(15 * time.Millisecond)
	results, timedOut, err := rt.executeToolCalls(wfCtx, "execute", engine.ActivityOptions{}, agent.Ident("agent-1"), runCtx, nil, calls, 0, nil, finishBy)
	require.NoError(t, err)
	require.True(t, timedOut)
	require.Len(t, results, 1)
	require.NotNil(t, results[0].Error)
	require.Equal(t, "canceled: time budget reached", results[0].Error.Message)
}
