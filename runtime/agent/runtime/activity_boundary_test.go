package runtime

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// These focused runtime doubles implement the agent helpers directly. Generic
// activity execution is covered by the real adapter conformance suite.
func (*testWorkflowContext) ExecuteActivity(context.Context, engine.ActivityCall, any) error {
	return errors.New("testWorkflowContext: unexpected generic activity")
}

func (*testWorkflowContext) ExecuteActivityAsync(context.Context, engine.ActivityCall) (engine.ActivityFuture, error) {
	return nil, errors.New("testWorkflowContext: unexpected generic activity")
}

func (*routeWorkflowContext) ExecuteActivity(context.Context, engine.ActivityCall, any) error {
	return errors.New("routeWorkflowContext: unexpected generic activity")
}

func (*routeWorkflowContext) ExecuteActivityAsync(context.Context, engine.ActivityCall) (engine.ActivityFuture, error) {
	return nil, errors.New("routeWorkflowContext: unexpected generic activity")
}

func (*stubEngine) RegisterActivity(context.Context, engine.ActivityDefinition) error {
	return errors.New("stubEngine: unexpected generic activity registration")
}

func (panicWorkflowContext) ExecuteActivity(context.Context, engine.ActivityCall, any) error {
	panic("unexpected generic activity from activity context")
}

func (panicWorkflowContext) ExecuteActivityAsync(context.Context, engine.ActivityCall) (engine.ActivityFuture, error) {
	panic("unexpected generic activity from activity context")
}

func (*cancelOnPlannerWorkflowContext) ExecuteActivity(context.Context, engine.ActivityCall, any) error {
	return errors.New("cancelOnPlannerWorkflowContext: unexpected generic activity")
}

func (*cancelOnPlannerWorkflowContext) ExecuteActivityAsync(context.Context, engine.ActivityCall) (engine.ActivityFuture, error) {
	return nil, errors.New("cancelOnPlannerWorkflowContext: unexpected generic activity")
}
