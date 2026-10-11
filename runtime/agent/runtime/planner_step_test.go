package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerActivityEventsCarryStep(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "start"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			published := make(chan hooks.Event, 7)
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				// Argument deltas bypass the durable log and hook bus; inspect their hook input instead.
				if input.Event.Type() == hooks.ToolCallArgsDelta {
					published <- input.Event
				}
				return nil, nil
			}}))
			subscription, err := rt.Bus.Register(hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
				published <- event
				return nil
			}))
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, subscription.Close())
			})
			require.NoError(t, rt.PromptRegistry.Register(prompt.PromptSpec{ID: "planner.prompt", AgentID: "content.agent", Role: prompt.PromptRoleUser, Template: "input"}))
			emit := func(ctx context.Context, agent planner.PlannerContext, events planner.PlannerEvents) (*planner.PlanResult, error) {
				events.AssistantChunk(ctx, "chunk")
				events.ToolCallArgsDelta(ctx, "call", "test.read", "{}")
				events.PlannerThought(ctx, "note", nil)
				events.UsageDelta(ctx, model.TokenUsage{Model: "provider", InputTokens: 1, TotalTokens: 1})
				events.PlannerThinkingBlock(ctx, model.ThinkingPart{Text: "thinking"})
				if _, err := agent.RenderPrompt(ctx, "planner.prompt", nil); err != nil {
					return nil, err
				}
				sink := events.(*runtimePlannerEvents)
				presentation := sink.StartModelPresentation(ctx)
				if err := sink.CommitModelPresentation(ctx, presentation, &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant}}}); err != nil {
					return nil, err
				}
				sink.FinishModelPresentation(ctx, presentation, true)
				return oracleFinal(), nil
			}
			registerContentAgent(t, rt, &stubPlanner{
				start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
					return emit(ctx, input.Agent, input.Events)
				},
				resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
					return emit(ctx, input.Agent, input.Events)
				},
			})
			input := &PlanActivityInput{AgentID: "content.agent", RunID: "root", Step: 2, RunContext: run.Context{RootRunID: "root", RunID: "root", SessionID: "session"}}
			_, err = executeContentPlannerActivity(t, rt, input, resume)
			require.NoError(t, err)
			page, err := rt.RunEventStore.List(t.Context(), "root", "", 100)
			require.NoError(t, err)
			recorded := page.Events
			require.Len(t, recorded, 6)
			assert.Len(t, published, 7)
			var kinds []hooks.EventType
			for len(published) > 0 {
				event := <-published
				kinds = append(kinds, event.Type())
				input, err := hooks.EncodeToHookInput(event, "")
				require.NoError(t, err)
				var metadata struct {
					PlannerStep *int `json:"planner_step,omitempty"`
				}
				require.NoError(t, json.Unmarshal(input.Payload, &metadata))
				require.NotNil(t, metadata.PlannerStep, "event %s", event.Type())
				assert.Equal(t, 2, *metadata.PlannerStep, "event %s", event.Type())
			}
			assert.Equal(t, []hooks.EventType{hooks.AssistantMessage, hooks.ToolCallArgsDelta, hooks.PlannerNote, hooks.Usage, hooks.ThinkingBlock, hooks.PromptRendered, hooks.AssistantTurnCommitted}, kinds)
		})
	}
}
