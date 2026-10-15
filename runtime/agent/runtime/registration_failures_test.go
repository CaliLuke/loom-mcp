package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registrationFailureEngine struct {
	engine.Engine
	fail  string
	calls []string
}

func TestRegisterAgentDoesNotPublishFailedRegistration(t *testing.T) {
	for _, stage := range []string{"hook", "workflow", "plan", "resume", "tool", "record", "summary", "recall", "child", "seal", "finish"} {
		t.Run(stage, func(t *testing.T) {
			eng := &registrationFailureEngine{Engine: inmem.New(), fail: stage}
			rt := New(WithEngine(eng))
			err := rt.RegisterAgent(t.Context(), contextRegistration(rt))
			require.ErrorIs(t, err, assert.AnError)
			assert.Empty(t, rt.ListAgents())
			require.NotEmpty(t, eng.calls)
			assert.Equal(t, stage, eng.calls[len(eng.calls)-1], "registration must stop at the engine failure")
		})
	}
}

func TestRegisterAgentRejectsIncompleteConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Runtime, *AgentRegistration)
		want   string
	}{
		{"agent id", func(_ *Runtime, r *AgentRegistration) {
			r.ID = ""
		}, "missing agent ID"},
		{"planner", func(_ *Runtime, r *AgentRegistration) {
			r.Planner = nil
		}, "missing planner"},
		{"workflow handler", func(_ *Runtime, r *AgentRegistration) {
			r.Workflow.Handler = nil
		}, "missing workflow handler"},
		{"plan activity", func(_ *Runtime, r *AgentRegistration) {
			r.PlanActivityName = ""
		}, "missing plan activity"},
		{"resume activity", func(_ *Runtime, r *AgentRegistration) {
			r.ResumeActivityName = ""
		}, "missing resume activity"},
		{"tool activity", func(_ *Runtime, r *AgentRegistration) {
			r.ExecuteToolActivity = ""
		}, "missing execute tool activity"},
		{"engine", func(rt *Runtime, _ *AgentRegistration) {
			rt.Engine = nil
		}, "engine"},
		{"unregistered interceptor", func(_ *Runtime, r *AgentRegistration) {
			r.Policy.NamedInterceptors = []string{"unknown"}
		}, "not registered"},
		{"memory scope", func(_ *Runtime, r *AgentRegistration) {
			r.Policy.PreloadMemory = &MemoryPreloadPolicy{}
		}, "requires a scope"},
		{"memory store", func(rt *Runtime, r *AgentRegistration) {
			rt.Memory = nil
			r.Policy.PreloadMemory = &MemoryPreloadPolicy{Scope: MemoryScopeCurrentRun}
		}, "WithMemoryStore"},
		{"invalid spec", func(_ *Runtime, r *AgentRegistration) {
			r.Specs = []tools.ToolSpec{{Name: "bad", TerminalRun: true}}
		}, "must also declare bookkeeping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := New(WithEngine(inmem.New()))
			registration := contextRegistration(rt)
			tt.change(rt, &registration)
			err := rt.RegisterAgent(t.Context(), registration)
			require.ErrorContains(t, err, tt.want)
			assert.Empty(t, rt.ListAgents())
		})
	}
}

func TestRegisterToolsetRejectsIncompleteConfiguration(t *testing.T) {
	execute := func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return nil, nil
	}
	for _, tt := range []struct {
		name string
		reg  ToolsetRegistration
		want string
	}{
		{"name", ToolsetRegistration{Execute: execute}, "name is required"},
		{"execute", ToolsetRegistration{Name: "tools"}, "execute function is required"},
		{"agent specs", ToolsetRegistration{Name: "tools", Execute: execute, AgentTool: &AgentToolConfig{}}, "requires tool specs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rt := New()
			before := rt.ListToolsets()
			err := rt.RegisterToolset(tt.reg)
			require.ErrorContains(t, err, tt.want)
			assert.Equal(t, before, rt.ListToolsets())
		})
	}
}

func (e *registrationFailureEngine) RegisterWorkflow(ctx context.Context, def engine.WorkflowDefinition) error {
	if err := e.visit("workflow"); err != nil {
		return err
	}
	return e.Engine.RegisterWorkflow(ctx, def)
}

func (e *registrationFailureEngine) RegisterHookActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.HookActivityInput) error) error {
	if err := e.visit("hook"); err != nil {
		return err
	}
	return e.Engine.RegisterHookActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterPlannerActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error)) error {
	if err := e.visit(name); err != nil {
		return err
	}
	return e.Engine.RegisterPlannerActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterExecuteToolActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ToolInput) (*api.ToolOutput, error)) error {
	if err := e.visit("tool"); err != nil {
		return err
	}
	return e.Engine.RegisterExecuteToolActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterRecordContentActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	if err := e.visit("record"); err != nil {
		return err
	}
	return e.Engine.RegisterRecordContentActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterSummarizeContextActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)) error {
	if err := e.visit("summary"); err != nil {
		return err
	}
	return e.Engine.RegisterSummarizeContextActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterRecallActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error)) error {
	if err := e.visit("recall"); err != nil {
		return err
	}
	return e.Engine.RegisterRecallActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterChildDispatchActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error)) error {
	if err := e.visit("child"); err != nil {
		return err
	}
	return e.Engine.RegisterChildDispatchActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterSealRecordActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	if err := e.visit("seal"); err != nil {
		return err
	}
	return e.Engine.RegisterSealRecordActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) RegisterFinishRunActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error)) error {
	if err := e.visit("finish"); err != nil {
		return err
	}
	return e.Engine.RegisterFinishRunActivity(ctx, name, opts, fn)
}

func (e *registrationFailureEngine) visit(stage string) error {
	e.calls = append(e.calls, stage)
	if e.fail == stage {
		return assert.AnError
	}
	return nil
}
