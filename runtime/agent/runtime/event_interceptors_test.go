package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type eventObserver struct{}

func (eventObserver) BeforeEvent(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
	return nil, nil
}
func (eventObserver) AfterEvent(context.Context, *AfterEventInput) (*AfterEventDecision, error) {
	return nil, nil
}

func TestHasEventInterceptors(t *testing.T) {
	before := func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
		return nil, nil
	}
	for _, tc := range []struct {
		name   string
		global []Interceptor
		local  []Interceptor
		want   bool
	}{
		{name: "none"},
		{name: "runtime", global: []Interceptor{RuntimeInterceptorFuncs{BeforeEventFunc: before}}, want: true},
		{name: "pointer", global: []Interceptor{&RuntimeInterceptorFuncs{BeforeEventFunc: before}}, want: true},
		{name: "agent", local: []Interceptor{eventObserver{}}, want: true},
		{name: "tool only", global: []Interceptor{RuntimeInterceptorFuncs{BeforeToolFunc: func(context.Context, *BeforeToolInput) (*BeforeToolDecision, error) {
			return nil, nil
		}}}},
		{name: "pointer tool only", global: []Interceptor{&RuntimeInterceptorFuncs{}}},
		{name: "retry reflect", global: []Interceptor{NewRetryAndReflectInterceptor(RetryAndReflectConfig{MaxRetries: 2})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithEngine(engineinmem.New()), WithInterceptors(tc.global...))
			require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{
				ID: agent.Ident("agent"), Planner: &stubPlanner{}, Interceptors: tc.local,
				Workflow:         engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
				PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
			}))
			assert.Equal(t, tc.want, rt.HasEventInterceptors())
		})
	}
}
