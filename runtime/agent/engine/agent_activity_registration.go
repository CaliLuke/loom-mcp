package engine

import (
	"context"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
)

// AgentActivityRegistrations supplies agent-specific helpers above the backend
// registration boundary. Adapters embed it with their ActivityRegistrar.
type AgentActivityRegistrations struct{ ActivityRegistrar }

const hookActivityClass = "hook"

// RegisterHookActivity registers the hook handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterHookActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.HookActivityInput) error) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}

// RegisterPlannerActivity registers the planner handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterPlannerActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: "planner", Options: opts, Handler: fn})
}

// RegisterExecuteToolActivity registers the tool handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterExecuteToolActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.ToolInput) (*api.ToolOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: "tool", Options: opts, Handler: fn})
}

// RegisterSealRecordActivity registers the seal handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterSealRecordActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}

// RegisterFinishRunActivity registers the finish handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterFinishRunActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}

// RegisterRecordContentActivity registers the record content handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterRecordContentActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}

// RegisterSummarizeContextActivity registers the summary handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterSummarizeContextActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: "planner", Options: opts, Handler: fn})
}

// RegisterRecallActivity registers the recall handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterRecallActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}

// RegisterChildDispatchActivity registers the child dispatch handler with its activity defaults.
func (a AgentActivityRegistrations) RegisterChildDispatchActivity(ctx context.Context, name string, opts ActivityOptions, fn func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error)) error {
	return a.RegisterActivity(ctx, ActivityDefinition{Name: name, Class: hookActivityClass, Options: opts, Handler: fn})
}
