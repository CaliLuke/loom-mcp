package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type (
	// RetryAndReflectConfig configures the retry-and-reflect interceptor.
	RetryAndReflectConfig struct {
		// MaxRetries is the number of tool failures that receive reflective retry
		// guidance before the policy stops handling the error. Defaults to 3.
		MaxRetries int
		// ErrorIfRetryExceeded returns the original tool execution error once the
		// retry budget is exhausted. When false, the final failure is still returned
		// as a tool result with retry guidance.
		ErrorIfRetryExceeded bool
	}

	retryAndReflectInterceptor struct {
		mu       sync.Mutex
		max      int
		failHard bool
		counts   map[string]int
	}
)

// NewRetryAndReflectInterceptor returns an interceptor that converts tool
// execution errors into planner-visible tool errors with structured retry
// guidance. This keeps the run alive so the planner can repair the call.
func NewRetryAndReflectInterceptor(cfg RetryAndReflectConfig) Interceptor {
	max := cfg.MaxRetries
	if max == 0 {
		max = 3
	}
	return &retryAndReflectInterceptor{
		max:      max,
		failHard: cfg.ErrorIfRetryExceeded,
		counts:   make(map[string]int),
	}
}

func (r *retryAndReflectInterceptor) BeforeTool(context.Context, *BeforeToolInput) (*BeforeToolDecision, error) {
	return nil, nil
}

func (r *retryAndReflectInterceptor) AfterTool(_ context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
	if input == nil || input.Err == nil {
		if input != nil {
			r.reset(input.Call)
		}
		return nil, nil
	}
	count := r.recordFailure(input.Call)
	if r.failHard && count > r.max {
		return nil, input.Err
	}
	result := &planner.ToolResult{
		Name:       input.Call.Name,
		ToolCallID: input.Call.ToolCallID,
		Error:      planner.NewToolError("tool execution failed; retry with corrected arguments"),
		RetryHint: &planner.RetryHint{
			Reason:         planner.RetryReasonInvalidArguments,
			Tool:           input.Call.Name,
			RestrictToTool: true,
			Message:        retryReflectMessage(input.Call.Name, count, r.max),
		},
	}
	return &AfterToolDecision{Result: result}, nil
}

func (r *retryAndReflectInterceptor) recordFailure(call planner.ToolRequest) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := retryReflectKey(call)
	r.counts[key]++
	return r.counts[key]
}

func (r *retryAndReflectInterceptor) reset(call planner.ToolRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.counts, retryReflectKey(call))
}

func retryReflectKey(call planner.ToolRequest) string {
	return call.RunID + "\x00" + string(call.Name)
}

func retryReflectMessage(tool tools.Ident, count, max int) string {
	return fmt.Sprintf(
		"Retry %s with corrected arguments. Attempt %d of %d failed.",
		tool,
		count,
		max,
	)
}
