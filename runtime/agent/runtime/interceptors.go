package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
)

type (
	// Interceptor observes or changes runtime call-path behavior.
	//
	// Interceptors are executed in registration order. They are separate from
	// hooks: hooks publish durable observability events, while interceptors run
	// inline before or after execution decisions are made.
	Interceptor interface {
		BeforeTool(context.Context, *BeforeToolInput) (*BeforeToolDecision, error)
		AfterTool(context.Context, *AfterToolInput) (*AfterToolDecision, error)
	}

	// ToolInterceptorFuncs adapts function fields into an Interceptor.
	ToolInterceptorFuncs struct {
		BeforeToolFunc func(context.Context, *BeforeToolInput) (*BeforeToolDecision, error)
		AfterToolFunc  func(context.Context, *AfterToolInput) (*AfterToolDecision, error)
	}

	// BeforeToolInput is passed to interceptors immediately before a tool
	// executor runs.
	BeforeToolInput struct {
		Call planner.ToolRequest
		Meta ToolCallMeta
	}

	// BeforeToolDecision changes the tool request or returns a result without
	// invoking the registered executor.
	BeforeToolDecision struct {
		Payload rawjson.Message
		Result  *ToolExecutionResult
	}

	// AfterToolInput is passed to interceptors after a tool executor returns.
	AfterToolInput struct {
		Call      planner.ToolRequest
		Meta      ToolCallMeta
		Result    *planner.ToolResult
		Execution *ToolExecutionResult
		Err       error
		Duration  time.Duration
	}

	// AfterToolDecision changes the executor outcome. Set Execution when the
	// interceptor needs to replace the full runtime envelope, or Result when only
	// the planner-visible tool result changes.
	AfterToolDecision struct {
		Execution *ToolExecutionResult
		Result    *planner.ToolResult
		Err       error
	}
)

// BeforeTool implements Interceptor.
func (f ToolInterceptorFuncs) BeforeTool(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
	if f.BeforeToolFunc == nil {
		return nil, nil
	}
	return f.BeforeToolFunc(ctx, input)
}

// AfterTool implements Interceptor.
func (f ToolInterceptorFuncs) AfterTool(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
	if f.AfterToolFunc == nil {
		return nil, nil
	}
	return f.AfterToolFunc(ctx, input)
}

func runBeforeToolInterceptors(ctx context.Context, interceptors []Interceptor, call planner.ToolRequest) (planner.ToolRequest, *ToolExecutionResult, error) {
	meta := toolCallMeta(call)
	for _, interceptor := range interceptors {
		if interceptor == nil {
			continue
		}
		decision, err := interceptor.BeforeTool(ctx, &BeforeToolInput{
			Call: call,
			Meta: meta,
		})
		if err != nil {
			return call, nil, err
		}
		if decision == nil {
			continue
		}
		if len(decision.Payload) > 0 {
			call.Payload = decision.Payload
			meta = toolCallMeta(call)
		}
		if decision.Result != nil {
			return call, decision.Result, nil
		}
	}
	return call, nil, nil
}

func runAfterToolInterceptors(
	ctx context.Context,
	interceptors []Interceptor,
	call planner.ToolRequest,
	exec *ToolExecutionResult,
	execErr error,
	duration time.Duration,
) (*ToolExecutionResult, error) {
	meta := toolCallMeta(call)
	current := exec
	currentErr := execErr
	for _, interceptor := range interceptors {
		if interceptor == nil {
			continue
		}
		var result *planner.ToolResult
		if current != nil {
			result = current.ToolResult
		}
		decision, err := interceptor.AfterTool(ctx, &AfterToolInput{
			Call:      call,
			Meta:      meta,
			Result:    result,
			Execution: current,
			Err:       currentErr,
			Duration:  duration,
		})
		if err != nil {
			return nil, err
		}
		if decision == nil {
			continue
		}
		current, currentErr = applyAfterToolDecision(current, decision)
	}
	if currentErr != nil {
		return nil, currentErr
	}
	if current == nil {
		return nil, errors.New("tool execution returned nil execution result")
	}
	return current, nil
}

func applyAfterToolDecision(current *ToolExecutionResult, decision *AfterToolDecision) (*ToolExecutionResult, error) {
	switch {
	case decision.Execution != nil:
		return decision.Execution, decision.Err
	case decision.Result != nil:
		if current == nil {
			current = &ToolExecutionResult{}
		}
		current.ToolResult = decision.Result
		return current, decision.Err
	default:
		return current, decision.Err
	}
}
