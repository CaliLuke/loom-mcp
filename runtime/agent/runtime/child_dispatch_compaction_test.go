package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildDispatchCompactedParentView(t *testing.T) {
	eng := &childDispatchRecordingEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng), WithContextCompaction("content.agent", ContextCompaction{
		TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30,
		ProtectRecentToolTokens: 40, MinSavingTokens: 20,
	}))
	base := compactionFixture(t, rt.RunStore, 30)
	messages, err := index.Render(t.Context(), base.Entries, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", hash)
	})
	require.NoError(t, err)
	provider := &recordingFactsClient{
		recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{}},
		limits:              model.ContextLimits{ContextWindow: 6000}, resolved: "fake",
	}
	require.NoError(t, rt.RegisterModel("fake", provider))
	lookup := newAnyJSONSpec("test.lookup", "test")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{lookup}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Result: rawjson.Message(`{"value":"ready"}`)}), nil
	}}))
	var validated, started atomic.Int32
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{
		ID: "child.agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
			assert.Greater(t, validated.Load(), started.Load(), "admission must precede the child start")
			started.Add(1)
			return oracleFinal(), nil
		}},
		Workflow:         engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "child.queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool",
	}))
	child := newAnyJSONSpec("child.lookup", "child")
	child.IsAgentTool = true
	reg := NewAgentToolsetRegistration(rt, AgentToolConfig{
		Name: "child", AgentID: "child.agent",
		Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"},
		PreChildValidator: func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
			loader := in.View.(*parentViewLoader)
			assert.Greater(t, loader.view.TailFrom, index.MessageID(1), "the parent must compact before dispatch")
			assert.NotNil(t, loader.view.Checkpoint)
			loaded, loadErr := in.View.LoadMessages(ctx)
			if assert.NoError(t, loadErr) {
				assert.Equal(t, true, loaded[0].Meta[transcript.CheckpointMetaKey])
				last := loaded[len(loaded)-1]
				assert.Equal(t, model.ConversationRoleAssistant, last.Role)
				assert.Len(t, last.Parts, 2, "both unanswered child calls remain visible")
				_, plannerErr := rt.loadView(ctx, "root", "root", loader.view)
				assert.EqualError(t, plannerErr, "compacted Anthropic grammar: view ends with unanswered tool calls", "the warm planner path must remain strict")
			}
			validated.Add(1)
			return nil
		},
	})
	reg.Specs = []tools.ToolSpec{child}
	require.NoError(t, rt.RegisterToolset(reg))
	registerContentAgent(t, rt, &stubPlanner{
		start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
			client, ok := in.Agent.ModelClient("fake")
			assert.True(t, ok)
			if _, callErr := client.Complete(ctx, &model.Request{Model: "fake", Messages: in.Messages}); callErr != nil {
				return nil, callErr
			}
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: lookup.Name, ToolCallID: "prepare", Payload: rawjson.Message(`{}`)}}}, nil
		},
		resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
			if len(in.ToolOutputs) == 1 {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
					{Name: child.Name, ToolCallID: "child-one", Payload: rawjson.Message(`{}`)},
					{Name: child.Name, ToolCallID: "child-two", Payload: rawjson.Message(`{}`)},
				}}, nil
			}
			assert.Len(t, in.ToolOutputs, 3)
			assert.Nil(t, in.ToolOutputs[1].Error)
			assert.Nil(t, in.ToolOutputs[2].Error)
			return oracleFinal(), nil
		},
	})
	_, err = rt.MustClient("content.agent").Run(t.Context(), "session", messages, WithRunID("root"))
	require.NoError(t, err)
	assert.Equal(t, int32(2), validated.Load())
	assert.Equal(t, int32(2), started.Load())
	require.Len(t, eng.inputs, 2)
	assert.NotEmpty(t, eng.inputs[0].View.Pending)
	assert.Empty(t, eng.inputs[1].View.Pending, "the second admission loads the adopted head")
	assert.NotEmpty(t, eng.outputs[0].IndexHead)
}

func TestChildAdmissionCompactedViewRejectsInvalidGrammar(t *testing.T) {
	call := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "pending", Name: "child.lookup", Input: rawjson.Message(`{}`)}}}
	oldCall := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "old", Name: "child.lookup", Input: rawjson.Message(`{}`)}}}
	text := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "next turn"}}}
	orphan := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "orphan", Content: "result"}}}
	for _, tc := range []struct {
		name    string
		tail    []*model.Message
		wantErr string
	}{
		{"non-trailing unanswered", []*model.Message{oldCall, call}, "compacted Anthropic grammar: view message 4: calls and results do not match"},
		{"orphan result", []*model.Message{text, orphan, call}, "compacted Anthropic grammar: view message 4: calls and results do not match"},
		{"ordering", []*model.Message{text, text}, `compacted Bedrock grammar: view message 4 repeats role "assistant"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 1)
			for _, message := range tc.tail {
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: compactionPut(t, rt.RunStore, message), Role: message.Role}))
			}
			view := base.view()
			view.TailFrom, view.Budget = 2, compactionBudget()
			loader := parentViewLoader{runtime: rt, parent: run.Context{RootRunID: "root", RunID: "run"}, view: view}
			loaded, err := loader.LoadMessages(t.Context())
			require.EqualError(t, err, tc.wantErr)
			assert.Nil(t, loaded)
			assert.Equal(t, err, loader.loadErr)
		})
	}
}
