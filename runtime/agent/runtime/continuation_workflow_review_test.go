package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reviewSealContext struct {
	*testWorkflowContext
	sealed *api.SealRecordInput
}

func TestContinuationWorkflowUnsuccessfulSkips(t *testing.T) {
	for _, failure := range []error{errors.New("after-run failed"), context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{AfterRunFunc: func(_ context.Context, in *AfterRunInput) (*AfterRunDecision, error) {
				require.NotNil(t, in.Output)
				require.NotEmpty(t, in.Output.FinalRef)
				return &AfterRunDecision{Output: in.Output, Err: failure}, nil
			}}))
			_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
			require.NoError(t, err)
			prior := continuationTestStart(t, rt, "prior", []*model.Message{contentMessage("prior")})
			continuationTestSeal(t, rt, "prior", prior, true)
			rt.agents["agent"] = AgentRegistration{ID: "agent", PlanActivityName: "plan"}
			wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{{Result: assistantCommitResult()}}}
			_, err = rt.ExecuteWorkflow(wf, &RunInput{AgentID: "agent", RunID: "run", RootRunID: "run", SessionID: "session", WorkflowProtocolVersion: api.WorkflowProtocolVersion, ContextCompaction: &ContextCompaction{}, Messages: []*model.Message{contentMessage("fresh")}})
			require.ErrorIs(t, err, failure)
			head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
			require.NoError(t, err)
			assert.Equal(t, "prior", head.ID)
			events, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			found := false
			for _, event := range events.Events {
				if event.Type == hooks.RunSealed {
					var sealed hooks.RunSealedEvent
					require.NoError(t, json.Unmarshal(event.Payload, &sealed))
					assert.Equal(t, "skipped", sealed.ContinuationOutcome)
					found = true
				}
			}
			require.True(t, found)
		})
	}
}

func TestContinuationSealPayloadDefaultUnchanged(t *testing.T) {
	rt := New()
	wf := &reviewSealContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context()}}
	base := &workflowPlanInput{RunContext: run.Context{RunID: "run", RootRunID: "run"}, protocolVersion: api.WorkflowProtocolVersion}
	base.compactionView = index.ViewRef{TailFrom: 7, ClearedThrough: 4, PreviewThrough: 6, ForceClearThrough: 3, Budget: index.RenderBudget{}}
	require.NoError(t, rt.sealWorkflowRecord(wf, &RunInput{AgentID: "agent", RunID: "run", RootRunID: "run", SessionID: "ordinary-session"}, base))
	data, err := json.Marshal(wf.sealed)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(data, &wire))
	// Exact pre-K18 key set from ff53086b's SealRecordInput; optional fields zero.
	expected := []string{"ContextSettings", "ContextRecovery", "RootRunID", "RunID", "ProtocolVersion", "IndexHead", "Pending", "EventKey", "Timestamp"}
	keys := make([]string, 0, len(wire))
	for key := range wire {
		keys = append(keys, key)
	}
	assert.ElementsMatch(t, expected, keys)
}

func (c *reviewSealContext) Detached() engine.WorkflowContext {
	return c
}
func (c *reviewSealContext) ExecuteSealRecordActivity(_ context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	c.sealed = call.Input
	return &api.SealRecordOutput{IndexHead: call.Input.IndexHead}, nil
}
