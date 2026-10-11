package trajectory_test

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptChildPlanner struct {
	interceptionPlanner
}

func TestBuildRealRuntimeAgentToolPromptPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rt := runtime.New(runtime.WithEngine(engineinmem.New()))
	require.NoError(t, rt.RegisterModel("registered", interceptionModel{}))
	require.NoError(t, rt.PromptRegistry.Register(prompt.PromptSpec{ID: "child.prompt", AgentID: "child", Role: prompt.PromptRoleUser, Template: "child input"}))
	require.NoError(t, rt.RegisterAgent(ctx, runtime.AgentRegistration{
		ID: "child", Planner: promptChildPlanner{},
		Workflow:         engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool",
	}))
	codec := tools.JSONCodec[any]{ToJSON: func(value any) ([]byte, error) {
		return json.Marshal(value)
	}, FromJSON: func(data []byte) (any, error) {
		var value any
		err := json.Unmarshal(data, &value)
		return value, err
	}}
	spec := tools.ToolSpec{Name: "test.read", Toolset: "test", IsAgentTool: true, AgentID: "child", Payload: tools.TypeSpec{Name: "payload", Codec: codec}, Result: tools.TypeSpec{Name: "result", Codec: codec}}
	registration := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{
		Name: "test", AgentID: "child", Route: runtime.AgentRoute{ID: "child", WorkflowName: "child.workflow", DefaultTaskQueue: "queue"},
		AgentToolContent: runtime.AgentToolContent{PromptSpecs: map[tools.Ident]prompt.Ident{"test.read": "child.prompt"}},
	})
	registration.Specs = []tools.ToolSpec{spec}
	require.NoError(t, rt.RegisterToolset(registration))
	require.NoError(t, rt.RegisterAgent(ctx, runtime.AgentRegistration{
		ID: "agent", Planner: interceptionPlanner{}, Specs: []tools.ToolSpec{spec},
		Workflow:         engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
	}))
	_, err := rt.CreateSession(ctx, "session")
	require.NoError(t, err)
	_, err = rt.MustClient("agent").Run(ctx, "session", nil, runtime.WithRunID("run"))
	require.NoError(t, err)
	rec, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "run", "run")
	require.NoError(t, err)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	promptIndex, toolIndex, secondModelIndex := -1, -1, -1
	for i, step := range got.Steps {
		if step.Tool != nil {
			toolIndex = i
			assert.Len(t, step.Tool.Children, 1)
		}
		if step.Model != nil && step.Step == 2 {
			secondModelIndex = i
		}
		if step.Event != nil && step.Event.Type == hooks.PromptRendered {
			promptIndex = i
			assert.Equal(t, 1, step.Step)
			var metadata struct {
				PlannerStep *int `json:"planner_step,omitempty"`
			}
			require.NoError(t, json.Unmarshal(step.Event.Payload, &metadata))
			assert.Nil(t, metadata.PlannerStep, "tool-side prompts must remain unstamped")
		}
	}
	require.GreaterOrEqual(t, promptIndex, 0)
	require.GreaterOrEqual(t, toolIndex, 0)
	require.GreaterOrEqual(t, secondModelIndex, 0)
	assert.Greater(t, promptIndex, toolIndex)
	assert.Less(t, promptIndex, secondModelIndex)
}

func (promptChildPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{FinalToolResult: &planner.FinalToolResult{Result: rawjson.Message(`{"whole":true}`)}}, nil
}
