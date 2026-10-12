package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverflowExpiredRetryPreservesUsage(t *testing.T) {
	for _, boundary := range []string{"start", "resume", "finalizer"} {
		t.Run(boundary, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 1)
			entry := &base.Entries[2]
			entry.Hash = compactionPut(t, rt.RunStore, strings.Repeat("large result ", 4000))
			entry.Bytes, entry.Result = 52000, &index.ResultProjection{ContentBytes: 52000}
			chain, err := index.ComputeChain(base.Entries[1].Chain, *entry)
			require.NoError(t, err)
			entry.Chain = chain
			overflowUsage := model.TokenUsage{InputTokens: 17, OutputTokens: 3, TotalTokens: 20}
			finalUsage := model.TokenUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}
			failure := contextmeasure.ContextOverflow{Source: contextmeasure.OverflowProvider, Failed: contextmeasure.RequestFacts{Model: "small", ViewBearing: true, ContextWindow: 8000, TextBytes: 52243, ViewTextBytes: 52243}}
			wf := &seedBudgetWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{{ContextOverflow: &failure, Usage: overflowUsage}, {Result: oracleFinal(), Usage: finalUsage}}}, now: time.Unix(1, 0), planDelay: 2 * time.Second}
			caps := policy.CapsState{}
			deadline := wf.Now().Add(time.Second)
			if boundary == "finalizer" {
				wf.resumeDelay = 2 * time.Second
				reg := AgentRegistration{ID: "agent", ResumeActivityName: "resume"}
				input := &RunInput{AgentID: "agent", RunID: "run", RootRunID: "root", WorkflowProtocolVersion: api.WorkflowProtocolVersion}
				output, usage, finalErr := rt.runFinalizationPlan(wf, reg, input, base, finalUsage, caps, 0, "turn", planner.TerminationReasonTimeBudget, deadline)
				require.ErrorIs(t, finalErr, context.DeadlineExceeded)
				assert.Nil(t, output)
				require.Len(t, wf.plannerCalls, 1, "expired finalizer retry must not schedule")
				assert.Equal(t, model.TokenUsage{InputTokens: 19, OutputTokens: 4, TotalTokens: 23}, usage)
				return
			}
			partial, retryErr := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, deadline, &caps, nil)
			require.ErrorIs(t, retryErr, context.DeadlineExceeded)
			require.Len(t, wf.plannerCalls, 1, "expired retry must not schedule")
			require.NotNil(t, partial, "usage from adopted overflow must survive")
			assert.Equal(t, overflowUsage, partial.Usage)
			reg := AgentRegistration{ID: "agent", ResumeActivityName: "resume"}
			input := &RunInput{AgentID: "agent", RunID: "run", RootRunID: "root", WorkflowProtocolVersion: api.WorkflowProtocolVersion}
			deadlines := runDeadlines{Budget: deadline, Hard: deadline.Add(time.Minute)}
			var output *RunOutput
			if boundary == "start" {
				output, _, err = rt.handleWorkflowStartError(wf, reg, input, workflowStartState{planInput: base, firstOutput: partial, deadlines: deadlines, caps: caps}, "turn", retryErr)
			} else {
				output, err = rt.finalizeAfterResumePlanError(wf, reg, input, base, &runLoopState{Caps: caps}, partial, &deadlines, "turn", planner.TerminationReasonTimeBudget)
			}
			require.NoError(t, err)
			require.NotNil(t, output.Usage)
			assert.Equal(t, model.TokenUsage{InputTokens: 19, OutputTokens: 4, TotalTokens: 23}, *output.Usage)
		})
	}
}
