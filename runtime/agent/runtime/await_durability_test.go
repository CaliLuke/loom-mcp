package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwaitStorageFailuresCannotBecomeSuccessfulRuns(t *testing.T) {
	for _, kind := range []string{"clarification", "typed", "external", "approve", "deny"} {
		t.Run(kind, func(t *testing.T) {
			baseline, _, err := runAwaitDurability(t, kind, 0, "")
			require.NoError(t, err)
			for i, operation := range baseline.operations {
				t.Run(fmt.Sprintf("%02d_%s", i+1, operation), func(t *testing.T) {
					failed, _, err := runAwaitDurability(t, kind, i+1, "")
					require.GreaterOrEqual(t, len(failed.operations), i+1)
					require.ErrorIs(t, err, assert.AnError)
				})
			}
		})
	}
}

func TestAwaitCanonicalEventFailuresStopRun(t *testing.T) {
	for _, kind := range []string{"clarification", "typed", "external", "approve", "deny"} {
		t.Run(kind, func(t *testing.T) {
			_, baseline, err := runAwaitDurability(t, kind, 0, "")
			require.NoError(t, err)
			seen := map[hooks.EventType]bool{}
			for _, event := range baseline.kinds {
				if seen[event] || event == hooks.RunCompleted {
					continue
				}
				seen[event] = true
				t.Run(string(event), func(t *testing.T) {
					_, failed, err := runAwaitDurability(t, kind, 0, event)
					assert.True(t, failed.rejected)
					require.ErrorIs(t, err, assert.AnError)
				})
			}
		})
	}
}

// runAwaitDurability answers actual await events through public signal APIs.
// The injected fault belongs to durable storage, never the workflow itself.
func runAwaitDurability(t *testing.T, kind string, failAt int, reject hooks.EventType, options ...RuntimeOption) (*durableWorkflowStore, *durableWorkflowLog, error) {
	t.Helper()
	bus := hooks.NewBus()
	rt := New(append([]RuntimeOption{WithoutContextDefault("content.agent"), WithHooks(bus)}, options...)...)
	store := &durableWorkflowStore{Store: rt.RunStore, failAt: failAt}
	log := &durableWorkflowLog{Store: rt.RunEventStore, reject: reject}
	rt.RunStore, rt.RunEventStore = store, log
	spec := newAnyJSONSpec("inline.read", "inline")
	if kind == "approve" || kind == "deny" {
		spec.Confirmation = &tools.ConfirmationSpec{PromptTemplate: "read?", DeniedResultTemplate: `{"denied":true}`}
	}
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "inline", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"found": true}}), nil
	}}))
	sub, err := bus.Register(hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		switch e := event.(type) {
		case *hooks.AwaitClarificationEvent:
			return rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: e.RunID(), ID: e.ID, Answer: "yes"})
		case *hooks.AwaitTypedInputEvent:
			return rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{RunID: e.RunID(), ID: e.ID, Payload: rawjson.Message(`{"answer":"yes"}`)})
		case *hooks.AwaitConfirmationEvent:
			return rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: e.RunID(), ID: e.ID, RequestedBy: "operator", Approved: kind == "approve"})
		case *hooks.AwaitExternalToolsEvent:
			return rt.ProvideToolResults(ctx, &api.ToolResultsSet{RunID: e.RunID(), ID: e.ID, Results: []*api.ProvidedToolResult{{Name: spec.Name, ToolCallID: "call", Result: rawjson.Message(`{"found":true}`)}}})
		}
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sub.Close())
	})
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		result := &planner.PlanResult{}
		switch kind {
		case "clarification":
			result.Await = planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"}))
		case "typed":
			result.Await = planner.NewAwait(planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "await", Schema: rawjson.Message(`{"type":"object"}`)}))
		case "external":
			result.Await = planner.NewAwait(planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "await", Items: []planner.AwaitToolItem{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}))
		default:
			result.ToolCalls = []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}
		}
		return result, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, err = rt.MustClient("content.agent").Run(ctx, "session", []*model.Message{contentMessage("look up the fact")}, WithRunID("root"))
	return store, log, err
}
