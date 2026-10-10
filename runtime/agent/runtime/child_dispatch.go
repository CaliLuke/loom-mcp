package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// ViewLoader loads an owned snapshot of the parent's canonical messages inside
// pre-child validation. A load error fails child dispatch even if ignored by the
// validator. It is scoped to the parent's root run and supplied view.
// Loading performs store I/O; validators must use the activity context.
type ViewLoader interface {
	LoadMessages(context.Context) ([]*model.Message, error)
}

type parentViewLoader struct {
	runtime *Runtime
	parent  run.Context
	view    index.ViewRef
	mu      sync.Mutex
	loadErr error
	head    runstore.Hash
}

// ChildDispatchActivityName returns the validation activity on the parent's queue.
func ChildDispatchActivityName(id agent.Ident) string {
	return string(id) + ".child_dispatch"
}

// ChildDispatchActivity runs application validation outside workflow execution.
// A structured failure becomes a tool result; an infrastructure error fails the
// activity. No child is admitted or started until the workflow receives success.
func (r *Runtime) ChildDispatchActivity(ctx context.Context, in *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("child dispatch input is required")
	}
	spec, ok := r.toolSpec(in.Call.Name)
	if !ok {
		return nil, fmt.Errorf("child dispatch tool %s is not registered", in.Call.Name)
	}
	r.mu.RLock()
	cfg := r.toolsets[spec.Toolset].AgentTool
	r.mu.RUnlock()
	if cfg == nil || cfg.PreChildValidator == nil {
		return nil, fmt.Errorf("child dispatch validator for %s is not registered", in.Call.Name)
	}
	payload, err := r.decodeAgentToolPromptPayload(ctx, in.Call)
	if err != nil {
		return r.childDispatchFailure(in.Call, err)
	}
	loader := &parentViewLoader{runtime: r, parent: in.ParentRun, view: in.View}
	validation := cfg.PreChildValidator(ctx, &AgentToolValidationInput{
		Call: &in.Call, Payload: payload, ParentRun: &in.ParentRun,
		View: loader,
	})
	loader.mu.Lock()
	loadErr := loader.loadErr
	head := loader.head
	loader.mu.Unlock()
	if loadErr != nil {
		return nil, loadErr
	}
	if validation != nil {
		out, err := r.childDispatchFailure(in.Call, validation)
		if err != nil {
			return nil, err
		}
		out.IndexHead = head
		return out, nil
	}
	return &api.ChildDispatchOutput{IndexHead: head}, nil
}

func (r *Runtime) childDispatchFailure(call planner.ToolRequest, err error) (*api.ChildDispatchOutput, error) {
	result, err := r.agentToolRequestFailureResult(call, err)
	if err != nil {
		return nil, err
	}
	return &api.ChildDispatchOutput{Error: result.Error.Error(), RetryHint: result.RetryHint}, nil
}

func (r *Runtime) validateChildDispatch(wf engine.WorkflowContext, id agent.Ident, cfg *AgentToolConfig, call *planner.ToolRequest, parent *run.Context, base *workflowPlanInput) (*planner.ToolResult, error) {
	if cfg.PreChildValidator == nil {
		return nil, nil
	}
	in := &api.ChildDispatchInput{Call: *call, ParentRun: *parent}
	if base != nil {
		in.View = base.view()
	}
	out, err := wf.ExecuteChildDispatchActivity(wf.Context(), engine.ChildDispatchActivityCall{Name: ChildDispatchActivityName(id), Input: in})
	if err != nil {
		return nil, err
	}
	if out.IndexHead != "" && base != nil {
		base.adoptHead(out.IndexHead)
	}
	if out.RetryHint == nil {
		return nil, nil
	}
	return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError(out.Error), RetryHint: out.RetryHint}, nil
}

func (l *parentViewLoader) LoadMessages(ctx context.Context) ([]*model.Message, error) {
	view, err := l.runtime.loadView(ctx, l.parent.RootRunID, l.parent.RunID, l.view)
	if err != nil {
		l.mu.Lock()
		l.loadErr = err
		l.mu.Unlock()
		return nil, err
	}
	if len(l.view.Pending) > 0 {
		l.mu.Lock()
		l.head = view.Head
		l.mu.Unlock()
	}
	return view.Messages, nil
}
