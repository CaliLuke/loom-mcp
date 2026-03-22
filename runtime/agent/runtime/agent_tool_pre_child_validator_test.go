package runtime

import (
	"context"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestExecuteToolCalls_AgentToolPreChildValidatorReturnsToolError(t *testing.T) {
	rt := &Runtime{
		toolsets:      map[string]ToolsetRegistration{},
		toolSpecs:     map[tools.Ident]tools.ToolSpec{},
		Bus:           noopHooks{},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	reg := NewAgentToolsetRegistration(rt, AgentToolConfig{
		AgentID: "svc.agent",
		Route: AgentRoute{
			ID:               agent.Ident("svc.agent"),
			WorkflowName:     "wf",
			DefaultTaskQueue: "default",
		},
		PreChildValidator: func(context.Context, *AgentToolValidationInput) *AgentToolValidationError {
			return NewAgentToolValidationError(
				"sources must come from prior evidence",
				[]*tools.FieldIssue{
					{
						Field:      "sources",
						Constraint: "invalid_format",
					},
				},
				map[string]string{
					"sources": "sources must come from prior evidence",
				},
			)
		},
	})
	rt.toolsets["svc.tools"] = reg
	spec := newAnyJSONSpec("svc.tools.do", "svc.tools")
	spec.IsAgentTool = true
	rt.toolSpecs["svc.tools.do"] = spec

	wfCtx := &testWorkflowContext{
		ctx:     context.Background(),
		runtime: rt,
	}
	results, _, err := rt.executeToolCalls(
		wfCtx,
		"execute",
		engine.ActivityOptions{},
		"agent-1",
		&run.Context{
			RunID:     "run-1",
			SessionID: "session-1",
			TurnID:    "turn-1",
		},
		nil,
		[]planner.ToolRequest{
			{
				Name:    "svc.tools.do",
				Payload: rawjson.Message([]byte(`{"sources":["x"]}`)),
			},
		},
		0,
		nil,
		time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotNil(t, results[0].Error)
	require.NotNil(t, results[0].RetryHint)
	require.Equal(t, planner.RetryReasonInvalidArguments, results[0].RetryHint.Reason)
	require.True(t, results[0].RetryHint.RestrictToTool)
	require.Contains(t, results[0].RetryHint.ClarifyingQuestion, "sources")
}
