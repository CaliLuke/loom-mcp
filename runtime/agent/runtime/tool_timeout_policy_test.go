package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timeoutPolicyEngine struct {
	engine.Engine
	mu    sync.Mutex
	calls []engine.ToolActivityCall
}

type timeoutPolicyContext struct {
	engine.WorkflowContext
	owner *timeoutPolicyEngine
}

func TestRunAppliesExactAndLongestPrefixToolTimeouts(t *testing.T) {
	eng := &timeoutPolicyEngine{Engine: inmem.New()}
	rt := New(WithEngine(eng), WithoutContextDefault("content.agent"))
	names := []tools.Ident{"docs.deep.exact", "docs.deep.other", "docs.read", "other.read", "zero.read", "docs.deep.again"}
	specs := make([]tools.ToolSpec, 0, len(names))
	calls := make([]planner.ToolRequest, 0, len(names))
	for _, name := range names {
		specs = append(specs, newAnyJSONSpec(name, "tools"))
		calls = append(calls, planner.ToolRequest{Name: name, ToolCallID: string(name), Payload: rawjson.Message(`{}`)})
	}
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "tools", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"ok": true}}), nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{ToolCalls: calls}, nil
	}, resume: func(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"), WithTiming(Timing{Tools: time.Minute, PerToolTimeout: map[tools.Ident]time.Duration{
		"docs.*": 20 * time.Second, "docs.deep.*": 30 * time.Second, "docs.deep.exact": 40 * time.Second, "zero.*": 0, "zzzz.*": 50 * time.Second,
	}}))
	require.NoError(t, err)
	eng.mu.Lock()
	defer eng.mu.Unlock()
	require.Len(t, eng.calls, 6)
	actual := make(map[tools.Ident]time.Duration)
	order := make([]tools.Ident, 0, len(eng.calls))
	for _, call := range eng.calls {
		actual[call.Input.ToolName] = call.Options.StartToCloseTimeout
		order = append(order, call.Input.ToolName)
	}
	assert.Equal(t, map[tools.Ident]time.Duration{"docs.deep.exact": 40 * time.Second, "docs.deep.other": 30 * time.Second, "docs.deep.again": 30 * time.Second, "docs.read": 20 * time.Second, "other.read": time.Minute, "zero.read": time.Minute}, actual)
	assert.Equal(t, []tools.Ident{"docs.deep.exact", "docs.deep.other", "docs.deep.again", "docs.read", "other.read", "zero.read"}, order)
}

func (e *timeoutPolicyEngine) RegisterWorkflow(ctx context.Context, def engine.WorkflowDefinition) error {
	handler := def.Handler
	def.Handler = func(wf engine.WorkflowContext, input *api.RunInput) (*api.RunOutput, error) {
		return handler(&timeoutPolicyContext{WorkflowContext: wf, owner: e}, input)
	}
	return e.Engine.RegisterWorkflow(ctx, def)
}

func (w *timeoutPolicyContext) Context() context.Context {
	return engine.WithWorkflowContext(w.WorkflowContext.Context(), w)
}

func (w *timeoutPolicyContext) Detached() engine.WorkflowContext {
	return &timeoutPolicyContext{WorkflowContext: w.WorkflowContext.Detached(), owner: w.owner}
}

func (w *timeoutPolicyContext) WithCancel() (engine.WorkflowContext, func()) {
	inner, cancel := w.WorkflowContext.WithCancel()
	return &timeoutPolicyContext{WorkflowContext: inner, owner: w.owner}, cancel
}

func (w *timeoutPolicyContext) ExecuteToolActivityAsync(ctx context.Context, call engine.ToolActivityCall) (engine.Future[*api.ToolOutput], error) {
	w.owner.mu.Lock()
	w.owner.calls = append(w.owner.calls, call)
	w.owner.mu.Unlock()
	return w.WorkflowContext.ExecuteToolActivityAsync(ctx, call)
}
