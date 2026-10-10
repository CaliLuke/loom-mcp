package runtime

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frozenIndexMessages reads expectations captured from the pre-K7 workflow,
// before its message mutation helpers were removed. Never regenerate from the loader.
func frozenIndexMessages(t *testing.T, key string) []*model.Message {
	t.Helper()
	data, err := os.ReadFile("testdata/k7_messages.json")
	require.NoError(t, err)
	fixtures := map[string]jsontext.Value{}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	data, ok := fixtures[key]
	require.True(t, ok, key)
	var messages []*model.Message
	require.NoError(t, json.Unmarshal(data, &messages))
	return messages
}

func assertIndexMessages(t *testing.T, want, got []*model.Message) {
	t.Helper()
	assert.Equal(t, string(canonicalIndexMessages(t, want)), string(canonicalIndexMessages(t, got)))
}

func oracleFinal() *planner.PlanResult {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}
}

// runIndexOracle drives the real workflow, activities, and client signal writers.
func runIndexOracle(t *testing.T, rt *Runtime, bus *recordingHooks, seed []*model.Message, answer func(hooks.Event) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	handle, err := rt.MustClient("content.agent").Start(ctx, "session", seed, WithRunID("root"), WithTurnID("turn"))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := handle.Wait(ctx)
		done <- err
	}()
	for {
		select {
		case err := <-done:
			require.NoError(t, err)
			return
		case event := <-bus.ch:
			if err := answer(event); err != nil {
				select {
				case runErr := <-done:
					t.Fatalf("signal %T: %v; run: %v", event, err, runErr)
				case <-ctx.Done():
					t.Fatalf("signal %T: %v", event, err)
				}
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestRuntimeIndexOracleTurnConfirmationAndAwait(t *testing.T) {
	bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
	rt := New(WithEngine(engineinmem.New()), WithHooks(bus))
	names := []tools.Ident{"test.plain", "test.confirm", "test.questions", "test.external"}
	specs := make([]tools.ToolSpec, len(names))
	for i, name := range names {
		specs[i] = newAnyJSONSpec(name, "test")
	}
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		assert.Equal(t, tools.Ident("test.plain"), call.Name)
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}}), nil
	}}))
	rt.toolConfirmation = &ToolConfirmationConfig{Confirm: map[tools.Ident]*ToolConfirmation{"test.confirm": {
		Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
			return "confirm", nil
		},
		DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) {
			return map[string]string{"summary": "denied"}, nil
		},
	}}}
	prefix := "TestIndexOracleTurnConfirmationAndAwaitScript/"
	seed := frozenIndexMessages(t, prefix+"3/workflow.go base init")

	expected := frozenIndexMessages(t, prefix+"7/workflow_await_queue.go confirmation pair without repeated transcript")
	qMessage := frozenIndexMessages(t, prefix+"8/workflow_await_publication.go questions")[7]
	eMessage := frozenIndexMessages(t, prefix+"9/workflow_await_publication.go external batch")[8]
	resumed := 0
	registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		assertIndexMessages(t, seed, in.Messages)
		in.Events.PlannerThinkingBlock(ctx, model.ThinkingPart{Text: "reason", Signature: "signed"})
		in.Events.AssistantChunk(ctx, "one turn")
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
			{Name: "test.plain", Payload: rawjson.Message(`{"q":"x"}`)},
			{Name: "test.confirm", ToolCallID: "confirm-1", Payload: rawjson.Message(`{}`)},
		}}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		resumed++
		assertIndexMessages(t, cloneMessages(expected), in.Messages)
		switch resumed {
		case 1:
			// The existing mixed confirmation path invokes resume again before
			// consuming its result; preserve that behavior in this oracle.
			return oracleFinal(), nil
		case 2:
			expected = append(expected, qMessage, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "questions-1", Content: map[string]any{"answer": "yes"}}}})
			return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "q", ToolName: "test.questions", ToolCallID: "questions-1", Payload: rawjson.Message(`{}`)}))}, nil
		case 3:
			expected = append(expected, eMessage, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "external-1", Content: map[string]any{"answer": "yes"}}, model.ToolResultPart{ToolUseID: "external-2", Content: map[string]any{"answer": "yes"}}}})
			return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "e", Items: []planner.AwaitToolItem{
				{Name: "test.external", ToolCallID: "external-1", Payload: rawjson.Message(`{"a":1}`)},
				{Name: "test.external", ToolCallID: "external-2", Payload: rawjson.Message(`{"a":2}`)},
			}}))}, nil
		default:
			assert.Len(t, in.ToolOutputs, 5)
			return oracleFinal(), nil
		}
	}})
	runIndexOracle(t, rt, bus, seed, func(event hooks.Event) error {
		switch e := event.(type) {
		case *hooks.AwaitConfirmationEvent:
			return rt.ProvideConfirmation(t.Context(), &api.ConfirmationDecision{RunID: "root", ID: e.ID, Approved: false, RequestedBy: "test"})
		case *hooks.AwaitQuestionsEvent:
			return rt.ProvideToolResults(t.Context(), &api.ToolResultsSet{RunID: "root", ID: e.ID, Results: []*api.ProvidedToolResult{{Name: e.ToolName, ToolCallID: e.ToolCallID, Result: rawjson.Message(`{"answer":"yes"}`)}}})
		case *hooks.AwaitExternalToolsEvent:
			results := make([]*api.ProvidedToolResult, len(e.Items))
			for i, item := range e.Items {
				results[i] = &api.ProvidedToolResult{Name: item.ToolName, ToolCallID: item.ToolCallID, Result: rawjson.Message(`{"answer":"yes"}`)}
			}
			return rt.ProvideToolResults(t.Context(), &api.ToolResultsSet{RunID: "root", ID: e.ID, Results: results})
		}
		return nil
	})
	assert.Equal(t, 4, resumed)
}

func TestRuntimeIndexOracleClarificationAndTyped(t *testing.T) {
	bus := &recordingHooks{ch: make(chan hooks.Event, 128)}
	eng := &indexCaptureEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng), WithHooks(bus))
	rounds := 0
	expected := frozenIndexMessages(t, "TestIndexOracleClarificationResumeAndTypedScript/2/workflow_await_wait.go clarification including empty answer")
	payload := rawjson.Message(`{"approved":true}`)
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{Await: planner.NewAwait(
			planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "nonempty", Question: "first"}),
			planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "empty", Question: "second"}),
			planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "typed", Schema: rawjson.Message(`{"type":"object"}`)}),
		)}, nil
	}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		rounds++
		assertIndexMessages(t, expected, in.Messages)
		require.Len(t, in.TypedInputs, 1)
		assert.Empty(t, in.TypedInputs[0].Payload)
		data, err := in.Content.LoadTypedInput(ctx, in.TypedInputs[0].PayloadRef)
		require.NoError(t, err)
		assert.JSONEq(t, string(payload), string(data))
		return oracleFinal(), nil
	}})
	runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
		switch e := event.(type) {
		case *hooks.AwaitClarificationEvent:
			answer := ""
			if e.ID == "nonempty" {
				answer = "clarification from await"
			}
			return rt.ProvideClarification(t.Context(), &api.ClarificationAnswer{RunID: "root", ID: e.ID, Answer: answer})
		case *hooks.AwaitTypedInputEvent:
			return rt.ProvideTypedInput(t.Context(), &api.TypedInputAnswer{RunID: "root", ID: e.ID, Payload: payload})
		}
		return nil
	})
	assert.Equal(t, 1, rounds)
	for _, in := range eng.inputs {
		data, err := json.Marshal(in)
		require.NoError(t, err)
		assert.Less(t, len(data), 4096+400*len(in.View.Pending))
		assert.NotContains(t, string(data), "clarification from await")
		assert.NotContains(t, string(data), "approved")
	}
}
