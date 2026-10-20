package engine

import (
	"context"
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
)

// AgentWorkflowActivities supplies the runtime's typed agent operations above
// the backend execution boundary. Each workflow scope binds its own executor.
type AgentWorkflowActivities struct{ ActivityExecutor }

// PublishHook executes the hook activity outside the workflow thread.
func (a AgentWorkflowActivities) PublishHook(ctx context.Context, call HookActivityCall) error {
	if err := validateAgentActivity("hook", call.Name, call.Input == nil); err != nil {
		return err
	}
	var ignored struct{}
	return a.ExecuteActivity(ctx, ActivityCall{Name: call.Name, Input: call.Input, Options: call.Options}, &ignored)
}

// ExecutePlannerActivity executes the planner activity with a typed result.
func (a AgentWorkflowActivities) ExecutePlannerActivity(ctx context.Context, call PlannerActivityCall) (*api.PlanActivityOutput, error) {
	if err := validateAgentActivity("planner", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.PlanActivityInput, *api.PlanActivityOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteToolActivity executes the tool activity with a typed result.
func (a AgentWorkflowActivities) ExecuteToolActivity(ctx context.Context, call ToolActivityCall) (*api.ToolOutput, error) {
	if err := validateAgentActivity("tool", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	future, err := a.ExecuteToolActivityAsync(ctx, call)
	if err != nil {
		return nil, err
	}
	return future.Get(ctx)
}

// ExecuteSealRecordActivity executes the seal activity with a typed result.
func (a AgentWorkflowActivities) ExecuteSealRecordActivity(ctx context.Context, call SealRecordActivityCall) (*api.SealRecordOutput, error) {
	if err := validateAgentActivity("seal", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.SealRecordInput, *api.SealRecordOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteFinishRunActivity executes the finish activity with a typed result.
func (a AgentWorkflowActivities) ExecuteFinishRunActivity(ctx context.Context, call FinishRunActivityCall) (*api.FinishRunOutput, error) {
	if err := validateAgentActivity("finish", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.FinishRunInput, *api.FinishRunOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteRecordContentActivity executes the record content activity with a typed result.
func (a AgentWorkflowActivities) ExecuteRecordContentActivity(ctx context.Context, call RecordContentActivityCall) (*api.RecordContentOutput, error) {
	if err := validateAgentActivity("record content", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.RecordContentInput, *api.RecordContentOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteSummarizeContextActivity executes the summary activity with a typed result.
func (a AgentWorkflowActivities) ExecuteSummarizeContextActivity(ctx context.Context, call SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	if err := validateAgentActivity("summary", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.SummarizeContextInput, *api.SummarizeContextOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteRecallActivity executes the recall activity with a typed result.
func (a AgentWorkflowActivities) ExecuteRecallActivity(ctx context.Context, call RecallActivityCall) (*api.RecallActivityOutput, error) {
	if err := validateAgentActivity("recall", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.RecallActivityInput, *api.RecallActivityOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteChildDispatchActivity executes the child dispatch activity with a typed result.
func (a AgentWorkflowActivities) ExecuteChildDispatchActivity(ctx context.Context, call ChildDispatchActivityCall) (*api.ChildDispatchOutput, error) {
	if err := validateAgentActivity("child dispatch", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.ChildDispatchInput, *api.ChildDispatchOutput]{Name: call.Name}).Execute(ctx, a.ActivityExecutor, call.Input, call.Options)
}

// ExecuteToolActivityAsync starts a tool activity without blocking the workflow.
func (a AgentWorkflowActivities) ExecuteToolActivityAsync(ctx context.Context, call ToolActivityCall) (Future[*api.ToolOutput], error) {
	if err := validateAgentActivity("tool", call.Name, call.Input == nil); err != nil {
		return nil, err
	}
	return (Activity[*api.ToolInput, *api.ToolOutput]{Name: call.Name}).ExecuteAsync(ctx, a.ActivityExecutor, call.Input, call.Options)
}

func validateAgentActivity(kind, name string, missingInput bool) error {
	if name == "" {
		return fmt.Errorf("%s activity name is required", kind)
	}
	if missingInput {
		return fmt.Errorf("%s activity input is required", kind)
	}
	return nil
}
