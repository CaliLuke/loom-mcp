package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type overflowAfterStreamPlanner struct {
	failure   error
	swallowed bool
}

func (p overflowAfterStreamPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	if _, err := consumePlannerStream(ctx, input.Agent, input.Events); err != nil {
		return nil, err
	}
	client, ok := input.Agent.ModelClient("overflow")
	if !ok {
		return nil, errors.New("overflow client missing")
	}
	_, err := client.Complete(ctx, &model.Request{Messages: input.Messages})
	if !p.swallowed {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: newTextAgentMessage(model.ConversationRoleAssistant, "discarded result")}}, nil
}

func (overflowAfterStreamPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func TestPlannerOverflowDiscardsAllPresentations(t *testing.T) {
	for _, swallowed := range []bool{false, true} {
		t.Run(stringBool(swallowed), func(t *testing.T) {
			sink := &recordingStreamSink{}
			log := inmem.New()
			rt := New(WithStream(sink), WithRunEventStore(log))
			_, err := rt.CreateSession(t.Context(), "session")
			require.NoError(t, err)
			failure := model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "overflow", "", false, nil)
			rt.models["default"] = newValidatedPresentationClient(t, "provisional")
			rt.models["overflow"] = &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, err: failure}
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: overflowAfterStreamPlanner{failure: failure, swallowed: swallowed}}
			out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), presentationPlanInput("run", "session"))
			require.NoError(t, err)
			require.NotNil(t, out.ContextOverflow)
			assert.Nil(t, out.Result)
			assert.Nil(t, out.Recovery)
			assert.Len(t, out.CallKeys, 2)
			assert.Equal(t, "provider", out.ContextOverflow.Source)
			assert.Empty(t, out.TranscriptRef)
			for _, event := range sink.snapshot() {
				assert.NotEqual(t, stream.EventAssistantTurn, event.Type())
			}
			events := sink.snapshot()
			require.NotEmpty(t, events)
			assert.Equal(t, stream.ModelPresentationDiscarded, events[len(events)-1].(stream.ModelPresentation).Data.State)
			record, err := runrecord.Read(t.Context(), rt.RunStore, log, "run", "run")
			require.NoError(t, err)
			assert.Empty(t, record.Runs[0].Presentations)
		})
	}
}

func stringBool(value bool) string {
	if value {
		return "swallowed"
	}
	return "returned"
}
