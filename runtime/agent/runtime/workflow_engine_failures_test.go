package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWorkflowEngine struct {
	engine.Engine
	fault *workflowEngineFault
}

type workflowEngineFault struct {
	mu     sync.Mutex
	calls  []string
	failAt int
}

type failingWorkflowContext struct {
	engine.WorkflowContext
	fault *workflowEngineFault
}

func TestWorkflowSchedulingFailuresCannotProduceSuccess(t *testing.T) {
	for _, kind := range []string{"clarification", "typed", "external", "approve", "deny"} {
		t.Run(kind, func(t *testing.T) {
			baseline := &workflowEngineFault{}
			_, _, err := runAwaitDurability(t, kind, 0, "", WithEngine(&failingWorkflowEngine{Engine: inmem.New(), fault: baseline}))
			require.NoError(t, err)
			for i, operation := range baseline.calls {
				t.Run(fmt.Sprintf("%02d_%s", i+1, operation), func(t *testing.T) {
					failure := &workflowEngineFault{failAt: i + 1}
					_, _, err := runAwaitDurability(t, kind, 0, "", WithEngine(&failingWorkflowEngine{Engine: inmem.New(), fault: failure}))
					require.GreaterOrEqual(t, len(failure.calls), i+1)
					require.ErrorIs(t, err, assert.AnError)
				})
			}
		})
	}
}

func (e *failingWorkflowEngine) RegisterWorkflow(ctx context.Context, def engine.WorkflowDefinition) error {
	handler := def.Handler
	def.Handler = func(wf engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		return handler(&failingWorkflowContext{WorkflowContext: wf, fault: e.fault}, in)
	}
	return e.Engine.RegisterWorkflow(ctx, def)
}

func (e *failingWorkflowEngine) SignalByID(ctx context.Context, id, execution, name string, payload any) error {
	return e.Engine.(engine.Signaler).SignalByID(ctx, id, execution, name, payload)
}

func (w *failingWorkflowContext) Context() context.Context {
	return engine.WithWorkflowContext(w.WorkflowContext.Context(), w)
}

func (w *failingWorkflowContext) Detached() engine.WorkflowContext {
	return &failingWorkflowContext{WorkflowContext: w.WorkflowContext.Detached(), fault: w.fault}
}

func (w *failingWorkflowContext) WithCancel() (engine.WorkflowContext, func()) {
	inner, cancel := w.WorkflowContext.WithCancel()
	return &failingWorkflowContext{WorkflowContext: inner, fault: w.fault}, cancel
}

func (w *failingWorkflowContext) SetQueryHandler(name string, handler any) error {
	if w.fault.fails("query " + name) {
		return assert.AnError
	}
	return w.WorkflowContext.SetQueryHandler(name, handler)
}

func (w *failingWorkflowContext) ExecutePlannerActivity(ctx context.Context, call engine.PlannerActivityCall) (*api.PlanActivityOutput, error) {
	if w.fault.fails("planner " + call.Name) {
		return nil, assert.AnError
	}
	return w.WorkflowContext.ExecutePlannerActivity(ctx, call)
}

func (w *failingWorkflowContext) ExecuteRecordContentActivity(ctx context.Context, call engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	if w.fault.fails("record") {
		return nil, assert.AnError
	}
	return w.WorkflowContext.ExecuteRecordContentActivity(ctx, call)
}

func (w *failingWorkflowContext) ExecuteSealRecordActivity(ctx context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	if w.fault.fails("seal") {
		return nil, assert.AnError
	}
	return w.WorkflowContext.ExecuteSealRecordActivity(ctx, call)
}

func (w *failingWorkflowContext) Await(ctx context.Context, condition func() bool) error {
	if w.fault.fails("await") {
		return assert.AnError
	}
	return w.WorkflowContext.Await(ctx, condition)
}

func (e *failingWorkflowEngine) QueryToolAwaits(ctx context.Context, id string) ([]planner.AwaitExternalTools, error) {
	return e.Engine.(engine.ToolAwaitQuerier).QueryToolAwaits(ctx, id)
}

func (f *workflowEngineFault) fails(operation string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, operation)
	return len(f.calls) == f.failAt
}
