package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rejectingContextClient struct {
	failure error
	calls   int
}
type rejectingContextPlanner struct{}

func (p *rejectingContextClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	p.calls++
	return nil, p.failure
}
func (p *rejectingContextClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}
func (rejectingContextPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := in.Agent.ModelClient("fake")
	if !ok {
		panic("registered fake model missing")
	}
	_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
	return nil, err
}
func (p rejectingContextPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.PlanStart(ctx, &planner.PlanInput{Agent: in.Agent, Messages: in.Messages})
}

// The real runtime activity and engine registration wrapper reach the SDK with
// MaxAttempts=3. Non-retryable provider errors must still execute exactly once.
func TestRuntimeProviderErrorDoesNotRetry(t *testing.T) {
	for _, kind := range []model.ProviderErrorKind{model.ProviderErrorKindAuth, model.ProviderErrorKindInvalidRequest, model.ProviderErrorKindUnknown} {
		t.Run(string(kind), func(t *testing.T) {
			env, eng := newStorageTestEnvironment(t)
			rt := agentsruntime.New()
			client := &rejectingContextClient{failure: model.NewProviderError("fake", "complete", 400, kind, "bad", "provider failure", "request", false, nil)}
			require.NoError(t, rt.RegisterModel("fake", client))
			require.NoError(t, rt.RegisterAgent(t.Context(), agentsruntime.AgentRegistration{ID: "agent", Planner: rejectingContextPlanner{}, Workflow: engine.WorkflowDefinition{Name: "unused", Handler: rt.ExecuteWorkflow}, PlanActivityName: "unused.plan", ResumeActivityName: "unused.resume", ExecuteToolActivity: "unused.tool"}))
			attempts := 0
			require.NoError(t, eng.RegisterPlannerActivity(t.Context(), "provider.plan", engine.ActivityOptions{StartToCloseTimeout: time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Millisecond}}, func(ctx context.Context, in *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
				attempts++
				out, err := rt.PlanStartActivity(ctx, in)
				var failure *engine.ApplicationError
				require.ErrorAs(t, err, &failure)
				assert.True(t, failure.NonRetryable)
				assert.Equal(t, "provider_error."+string(kind), failure.Type)
				return out, err
			}))
			require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "provider.workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
				_, err := wf.ExecutePlannerActivity(wf.Context(), engine.PlannerActivityCall{Name: "provider.plan", Input: &api.PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: run.Context{RootRunID: "root", RunID: "run"}}})
				return nil, err
			}}))
			env.ExecuteWorkflow("provider.workflow", &api.RunInput{RunID: "run"})
			require.ErrorContains(t, env.GetWorkflowError(), "provider failure")
			assert.Equal(t, 1, attempts)
			assert.Equal(t, 1, client.calls)
		})
	}
}
