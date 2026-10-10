package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type childDispatchMarker struct{}
type childDispatchRecordingEngine struct {
	engine.Engine
	calls   int
	queue   string
	inputs  []*api.ChildDispatchInput
	outputs []*api.ChildDispatchOutput
}

func (e *childDispatchRecordingEngine) RegisterChildDispatchActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error)) error {
	e.queue = opts.Queue
	return e.Engine.RegisterChildDispatchActivity(ctx, name, opts, func(ctx context.Context, in *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
		e.calls++
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		var recorded api.ChildDispatchInput
		if err := json.Unmarshal(data, &recorded); err != nil {
			return nil, err
		}
		e.inputs = append(e.inputs, &recorded)
		out, err := fn(context.WithValue(ctx, childDispatchMarker{}, true), in)
		e.outputs = append(e.outputs, out)
		return out, err
	})
}

func TestChildDispatchValidationBeforeChildStart(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "accepted"
		if reject {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			eng := &childDispatchRecordingEngine{Engine: engineinmem.New()}
			rt := New(WithEngine(eng))
			validated, started := false, false
			require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{ID: "child.agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				assert.True(t, validated)
				started = true
				return oracleFinal(), nil
			}}, Workflow: engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "child.queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool"}))
			spec := newAnyJSONSpec("child.lookup", "child")
			spec.IsAgentTool = true
			reg := NewAgentToolsetRegistration(rt, AgentToolConfig{Name: "child", AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"}, PreChildValidator: func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
				assert.Equal(t, true, ctx.Value(childDispatchMarker{}), "validator must execute inside registered activity")
				assert.False(t, started)
				validated = true
				assert.Equal(t, "root", in.ParentRun.RootRunID)
				assert.Equal(t, "child-call", in.Call.ToolCallID)
				assert.Equal(t, map[string]any{"query": "evidence"}, in.Payload)
				messages, err := in.View.LoadMessages(ctx)
				require.NoError(t, err)
				assertIndexMessages(t, []*model.Message{contentMessage("parent evidence"), {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "child-call", Name: string(spec.Name), Input: rawjson.Message(`{"query":"evidence"}`)}}}}, messages)
				if reject {
					return NewAgentToolValidationError("declined", []*tools.FieldIssue{{Field: "query", Constraint: "invalid_format"}}, nil)
				}
				return nil
			}})
			reg.Specs = []tools.ToolSpec{spec}
			require.NoError(t, rt.RegisterToolset(reg))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "child-call", Payload: rawjson.Message(`{"query":"evidence"}`)}}}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				require.Len(t, in.ToolOutputs, 1)
				assert.Equal(t, reject, in.ToolOutputs[0].Error != nil)
				return oracleFinal(), nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("parent evidence")}, WithRunID("root"))
			require.NoError(t, err)
			assert.Equal(t, !reject, started)
			assert.True(t, validated)
			assert.Equal(t, 1, eng.calls)
			assert.Equal(t, rt.agents["content.agent"].Workflow.TaskQueue, eng.queue)
			require.Len(t, eng.inputs, 1)
			assert.NotEmpty(t, eng.inputs[0].View.IndexHead)
			require.Len(t, eng.inputs[0].View.Pending, 1)
		})
	}
}

func TestParentViewLoaderMissingContent(t *testing.T) {
	rt := New()
	loader := parentViewLoader{runtime: rt, parent: run.Context{RootRunID: "root", RunID: "parent"}, view: index.ViewRef{IndexHead: "sha256:missing"}}
	_, err := loader.LoadMessages(t.Context())
	require.ErrorIs(t, err, runstore.ErrNotFound)
}

func TestChildDispatchFailsOnIgnoredViewLoadError(t *testing.T) {
	rt := New()
	spec := newAnyJSONSpec("child.lookup", "child")
	reg := NewAgentToolsetRegistration(rt, AgentToolConfig{Name: "child", AgentID: "child.agent", Route: AgentRoute{ID: "child.agent", WorkflowName: "child.workflow", DefaultTaskQueue: "child.queue"}, PreChildValidator: func(ctx context.Context, in *AgentToolValidationInput) *AgentToolValidationError {
		messages, err := in.View.LoadMessages(ctx)
		require.ErrorIs(t, err, runstore.ErrNotFound)
		assert.Nil(t, messages)
		return nil // A callback cannot approve a child using an unreadable view.
	}})
	reg.Specs = []tools.ToolSpec{spec}
	require.NoError(t, rt.RegisterToolset(reg))
	out, err := rt.ChildDispatchActivity(t.Context(), &api.ChildDispatchInput{Call: planner.ToolRequest{Name: spec.Name}, ParentRun: run.Context{RootRunID: "root", RunID: "parent"}, View: index.ViewRef{IndexHead: "sha256:missing"}})
	require.ErrorIs(t, err, runstore.ErrNotFound)
	assert.Nil(t, out)
}
