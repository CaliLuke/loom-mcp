package agentfeatures_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"example.com/agentfeatures/gen/features/agents/specialist"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelCallBudgetPlanner struct {
	calls      int
	retryFirst bool
	attempts   atomic.Int32
}

func TestGeneratedModelCallBudget(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		calls, override, admitted int
		refused                   bool
	}{
		{"generated default", 4, 0, 3, true},
		{"override replaces default", 4, 2, 2, true},
		{"last permitted call succeeds", 3, 0, 3, false},
		{"larger override", 4, 5, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFeatureRuntime(t)
			p := &modelCallBudgetPlanner{calls: tc.calls}
			providerCalls := registerModelCallBudgetAgent(t, fx, p)
			_, err := fx.rt.CreateSession(t.Context(), "model-budget-session")
			require.NoError(t, err)
			opts := []agentsruntime.RunOption{agentsruntime.WithRunID("model-budget-run")}
			if tc.override > 0 {
				opts = append(opts, agentsruntime.WithRunMaxModelCalls(tc.override))
			}
			out, err := specialist.NewClient(fx.rt).Run(t.Context(), "model-budget-session", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "bounded work"}}}}, opts...)
			if tc.refused {
				requireModelCallLimit(t, err)
				assert.Nil(t, out)
				record, err := runrecord.Read(t.Context(), fx.rt.RunStore, fx.rt.RunEventStore, "model-budget-run", "model-budget-run")
				require.NoError(t, err)
				require.NotNil(t, record.Runs[0].ModelCallLimit)
				assert.Equal(t, tc.admitted, record.Runs[0].ModelCallLimit.Limit)
				assert.Equal(t, tc.admitted+1, record.Runs[0].ModelCallLimit.Call.Seq)
				assert.Len(t, record.Runs[0].Calls, tc.admitted, "refusal never invents a provider call")
			} else {
				require.NoError(t, err)
				require.NotNil(t, out)
				assert.Equal(t, "model budget complete", messageText(out.Final))
			}
			assert.EqualValues(t, tc.admitted, providerCalls.Load())
			assert.Equal(t, int32(1), p.attempts.Load())
		})
	}
}

func registerModelCallBudgetAgent(t *testing.T, fx *featureRuntime, p *modelCallBudgetPlanner) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	require.NoError(t, fx.rt.RegisterModel("model-budget", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		calls.Add(1)
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "admitted"}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, specialist.RegisterSpecialistAgent(t.Context(), fx.rt, specialist.SpecialistAgentConfig{Planner: p}))
	return calls
}

func requireModelCallLimit(t *testing.T, err error) {
	t.Helper()
	var application *engine.ApplicationError
	require.ErrorAs(t, err, &application)
	assert.Equal(t, "loom_mcp.model_call_cap", application.Type)
	assert.True(t, application.NonRetryable)
}

func (p *modelCallBudgetPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	attempt := p.attempts.Add(1)
	client, ok := in.Agent.ModelClient("model-budget")
	if !ok {
		return nil, fmt.Errorf("missing model-budget client")
	}
	for i := 0; i < p.calls; i++ {
		_, err := client.Complete(ctx, &model.Request{Model: "fixture", Messages: in.Messages})
		if err != nil && !errors.Is(err, policy.ErrModelCallLimit) {
			return nil, err
		}
		if p.retryFirst && attempt == 1 {
			return nil, errors.New("retry after the first reserved model call")
		}
	}
	// Deliberately swallow refusal: sticky activity evidence must override success.
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "model budget complete"}}}}}, nil
}

func (*modelCallBudgetPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("model budget must not resume or finalize")
}
