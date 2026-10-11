package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type loopPreparationLogger struct {
	telemetry.NoopLogger
	prepared bool
}

func TestWorkflowPreparesLoopBeforeExecutingPhase(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared_before_batch", true: "invalid_result_has_no_execution_phase"}[invalid], func(t *testing.T) {
			logger := &loopPreparationLogger{}
			rt := New(WithLogger(logger))
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{}, PlanActivityName: "plan"}
			phases := 0
			rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				if event, ok := input.Event.(*hooks.RunPhaseChangedEvent); ok && event.Phase == run.PhaseExecutingTools {
					phases++
					assert.True(t, logger.prepared, "pure loop-state preparation precedes the batched phase publication")
				}
				return nil, nil
			}}}
			result := assistantCommitResult()
			if invalid {
				result = &planner.PlanResult{}
			}
			wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{{Result: result}}}
			_, err := rt.ExecuteWorkflow(wf, &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion})
			if invalid {
				require.Error(t, err)
				assert.Zero(t, phases)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, phases)
			}
		})
	}
}

// Info observes the preparation boundary without replacing workflow computation.
func (l *loopPreparationLogger) Info(_ context.Context, message string, _ ...any) {
	if message == "PlanResult has a terminal result but no ToolCalls - workflow will return early" {
		l.prepared = true
	}
}
