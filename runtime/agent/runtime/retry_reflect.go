package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// retryReflectFailureLimit bounds process-local retry state because workflow
// completion can run in another worker process.
const retryReflectFailureLimit = 4096

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

	retryReflectKey struct {
		runID string
		tool  tools.Ident
	}

	retryReflectFailure struct {
		count    int
		sequence uint64
	}

	retryReflectOrderEntry struct {
		key      retryReflectKey
		sequence uint64
	}

	retryAndReflectInterceptor struct {
		mu       sync.Mutex
		max      int
		failHard bool
		counts   map[retryReflectKey]retryReflectFailure
		order    []retryReflectOrderEntry
		next     int
		sequence uint64
	}
)

// NewRetryAndReflectInterceptor returns an interceptor that converts tool
// execution errors into planner-visible tool errors with structured retry
// guidance. This keeps the run alive so the planner can repair the call.
// Failure tracking retains at most retryReflectFailureLimit recent run/tool keys
// in each process.
func NewRetryAndReflectInterceptor(cfg RetryAndReflectConfig) Interceptor {
	max := cfg.MaxRetries
	if max == 0 {
		max = 3
	}
	return &retryAndReflectInterceptor{
		max:      max,
		failHard: cfg.ErrorIfRetryExceeded,
		counts:   make(map[retryReflectKey]retryReflectFailure),
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
	key := retryReflectKey{runID: call.RunID, tool: call.Name}
	count := 1
	if failure, ok := r.counts[key]; ok {
		count = failure.count + 1
	}

	r.sequence++
	failure := retryReflectFailure{count: count, sequence: r.sequence}
	orderEntry := retryReflectOrderEntry{key: key, sequence: r.sequence}
	if len(r.order) < retryReflectFailureLimit {
		r.order = append(r.order, orderEntry)
	} else {
		expired := r.order[r.next]
		if current, ok := r.counts[expired.key]; ok && current.sequence == expired.sequence {
			delete(r.counts, expired.key)
		}
		r.order[r.next] = orderEntry
		r.next = (r.next + 1) % retryReflectFailureLimit
	}
	r.counts[key] = failure
	return count
}

func (r *retryAndReflectInterceptor) reset(call planner.ToolRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.counts, retryReflectKey{runID: call.RunID, tool: call.Name})
}

func retryReflectMessage(tool tools.Ident, count, max int) string {
	return fmt.Sprintf(
		"Retry %s with corrected arguments. Attempt %d of %d failed.",
		tool,
		count,
		max,
	)
}
