package dsl

import (
	expragents "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// RunPolicy defines execution constraints for the current agent. Use RunPolicy
// to configure resource limits, timeouts, history management, and runtime
// behaviors that govern how the agent executes. These policies are enforced by
// the runtime during agent execution.
//
// RunPolicy must appear in an Agent expression.
//
// RunPolicy takes a single argument which is the defining DSL function.
//
// The DSL function may use:
//   - DefaultCaps to set capability limits (tool calls, consecutive failures)
//   - TimeBudget to set maximum execution duration
//   - InterruptsAllowed to enable or disable user interruptions
//   - OnMissingFields to configure validation behavior
//   - History to configure how conversation history is truncated or compressed
//   - Cache to configure prompt caching hints for supported providers
//
// Example:
//
//	Agent("assistant", "Helper agent", func() {
//	    RunPolicy(func() {
//	        DefaultCaps(MaxToolCalls(10), MaxConsecutiveFailedToolCalls(3))
//	        TimeBudget("5m")
//	        InterruptsAllowed(true)
//	        OnMissingFields("await_clarification")
//	        History(func() {
//	            KeepRecentTurns(20)
//	        })
//	    })
//	})
func RunPolicy(fn func()) {
	agent, ok := eval.Current().(*expragents.AgentExpr)
	if !ok {
		incompatibleDSL("RunPolicy")
		return
	}
	policy := agent.RunPolicy
	if policy == nil {
		policy = &expragents.RunPolicyExpr{
			Agent: agent,
		}
		agent.RunPolicy = policy
	}
	if fn != nil {
		eval.Execute(fn, policy)
	}
}

// DefaultCaps configures resource limits for agent execution. Use DefaultCaps
// to control how many tools the agent can invoke and how many consecutive
// failures are tolerated before stopping execution.
//
// DefaultCaps must appear in a RunPolicy expression.
//
// DefaultCaps takes zero or more CapsOption arguments (created via MaxToolCalls
// and MaxConsecutiveFailedToolCalls).
//
// Example:
//
//	RunPolicy(func() {
//	    DefaultCaps(
//	        MaxToolCalls(20),
//	        MaxConsecutiveFailedToolCalls(3),
//	    )
//	})
func DefaultCaps(opts ...CapsOption) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("DefaultCaps")
		return
	}
	caps := policy.DefaultCaps
	if caps == nil {
		caps = &expragents.CapsExpr{Policy: policy}
		policy.DefaultCaps = caps
	}
	for _, opt := range opts {
		if opt != nil {
			opt(caps)
		}
	}
}

// TimeBudget sets the active planner/tool work budget for the agent. Time spent
// awaiting clarification, confirmation, typed input, or external tool results
// does not consume the budget, so elapsed wall time may be longer.
//
// TimeBudget must appear in a RunPolicy expression.
//
// TimeBudget takes a single argument which is a duration string (e.g., "30s",
// "5m", "1h").
//
// Example:
//
//	RunPolicy(func() {
//	    TimeBudget("5m")
//	})
func TimeBudget(duration string) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("TimeBudget")
		return
	}
	dur, ok := parsePositiveDuration("TimeBudget", duration)
	if !ok {
		return
	}
	policy.TimeBudget = dur
}

// InterruptsAllowed configures whether user interruptions are permitted during
// agent execution. When enabled, users can interrupt running agents to provide
// guidance or stop execution.
//
// InterruptsAllowed must appear in a RunPolicy expression.
//
// InterruptsAllowed takes a single boolean argument.
//
// Example:
//
//	RunPolicy(func() {
//	    InterruptsAllowed(true)
//	})
func InterruptsAllowed(allowed bool) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("InterruptsAllowed")
		return
	}
	policy.InterruptsAllowed = allowed
}

// OnMissingFields configures how the agent responds when tool invocation
// validation detects missing required fields. This allows you to control
// whether the agent should stop, wait for user input, or continue execution.
//
// OnMissingFields must appear in a RunPolicy expression.
//
// OnMissingFields takes a single string argument. Valid values:
//   - "finalize": stop execution when required fields are missing
//   - "await_clarification": pause and wait for user to provide missing information
//   - "resume": continue execution despite missing fields
//   - "" (empty): let the planner decide based on context
//
// Example:
//
//	RunPolicy(func() {
//	    OnMissingFields("await_clarification")
//	})
func OnMissingFields(action string) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("OnMissingFields")
		return
	}
	switch action {
	case "", "finalize", "await_clarification", "resume":
		policy.OnMissingFields = action
	default:
		eval.ReportError("invalid OnMissingFields value %q (allowed: finalize, await_clarification, resume)", action)
	}
}

// CapsOption defines a functional option for configuring per-run resource limits
// on agent execution.
type CapsOption func(*expragents.CapsExpr)

// PreloadMemoryOption configures bounded memory preload.
type PreloadMemoryOption interface {
	applyPreloadMemory(*expragents.MemoryPreloadExpr)
}

// PreloadLongTermMemoryOption configures bounded long-term memory preload.
type PreloadLongTermMemoryOption interface {
	applyPreloadLongTermMemory(*expragents.LongTermMemoryPreloadExpr)
}

type memoryScopeOption expragents.MemoryScope

// RetryAndReflectOption configures RetryAndReflect policy behavior.
type RetryAndReflectOption func(*expragents.RetryAndReflectExpr)

// Interceptors declares application-owned interceptor IDs for this agent.
//
// Interceptors must appear in a RunPolicy expression.
func Interceptors(ids ...string) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("Interceptors")
		return
	}
	policy.Interceptors = append(policy.Interceptors, ids...)
}

// PreloadMemory enables bounded memory snippets in planner inputs.
//
// PreloadMemory must appear in a RunPolicy expression.
func PreloadMemory(opts ...PreloadMemoryOption) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("PreloadMemory")
		return
	}
	preload := policy.PreloadMemory
	if preload == nil {
		preload = &expragents.MemoryPreloadExpr{Policy: policy}
		policy.PreloadMemory = preload
	}
	for _, opt := range opts {
		if opt != nil {
			opt.applyPreloadMemory(preload)
		}
	}
}

// PreloadLongTermMemory enables bounded long-term memory entries in planner inputs.
//
// PreloadLongTermMemory must appear in a RunPolicy expression.
func PreloadLongTermMemory(opts ...PreloadLongTermMemoryOption) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("PreloadLongTermMemory")
		return
	}
	preload := policy.PreloadLongTermMemory
	if preload == nil {
		preload = &expragents.LongTermMemoryPreloadExpr{
			Policy:     policy,
			Visibility: expragents.MemoryVisibilityUser,
		}
		policy.PreloadLongTermMemory = preload
	}
	for _, opt := range opts {
		if opt != nil {
			opt.applyPreloadLongTermMemory(preload)
		}
	}
}

// MemoryScopeCurrentRun selects current-run memory for preload.
func MemoryScopeCurrentRun() memoryScopeOption {
	return memoryScopeOption(expragents.MemoryScopeCurrentRun)
}

// MemoryScopeIndexed selects indexed memory for preload.
func MemoryScopeIndexed() memoryScopeOption {
	return memoryScopeOption(expragents.MemoryScopeIndexed)
}

func (o memoryScopeOption) applyPreloadMemory(preload *expragents.MemoryPreloadExpr) {
	preload.Scope = expragents.MemoryScope(o)
}

func (o memoryMaxResultsOption) applyPreloadMemory(preload *expragents.MemoryPreloadExpr) {
	preload.MaxResults = int(o)
}

func (o memoryMaxResultsOption) applyPreloadLongTermMemory(preload *expragents.LongTermMemoryPreloadExpr) {
	preload.MaxResults = int(o)
}

func (o memoryVisibilityOption) applyPreloadLongTermMemory(preload *expragents.LongTermMemoryPreloadExpr) {
	preload.Visibility = expragents.MemoryVisibility(o)
}

// RetryAndReflect enables tool-error reflection. When a tool executor returns
// an error, the runtime converts it into a planner retry hint so the model can
// repair the call arguments instead of immediately failing the run.
//
// RetryAndReflect must appear in a RunPolicy expression.
func RetryAndReflect(opts ...RetryAndReflectOption) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("RetryAndReflect")
		return
	}
	retry := policy.RetryAndReflect
	if retry == nil {
		retry = &expragents.RetryAndReflectExpr{Policy: policy}
		policy.RetryAndReflect = retry
	}
	for _, opt := range opts {
		if opt != nil {
			opt(retry)
		}
	}
}

// MaxRetries configures how many reflected retry hints are allowed for a
// run/tool pair. Zero uses the runtime default.
func MaxRetries(n int) RetryAndReflectOption {
	return func(r *expragents.RetryAndReflectExpr) {
		r.MaxRetries = n
	}
}

// ErrorIfRetryExceeded configures whether the original tool error should be
// returned once the reflected retry budget has been consumed.
func ErrorIfRetryExceeded(enabled bool) RetryAndReflectOption {
	return func(r *expragents.RetryAndReflectExpr) {
		r.ErrorIfRetryExceeded = enabled
	}
}

// MaxToolCalls configures the maximum number of tool invocations allowed during
// agent execution. Use this with DefaultCaps to limit total tool usage.
//
// MaxToolCalls takes a single integer argument specifying the maximum count.
//
// Example:
//
//	DefaultCaps(MaxToolCalls(15))
func MaxToolCalls(n int) CapsOption {
	return func(c *expragents.CapsExpr) {
		if n <= 0 {
			eval.ReportError("MaxToolCalls requires n > 0, got %d", n)
			return
		}
		c.MaxToolCalls = n
	}
}

// MaxConsecutiveFailedToolCalls configures the maximum number of consecutive
// tool failures before the agent stops execution. Use this with DefaultCaps to
// prevent runaway failure loops.
//
// MaxConsecutiveFailedToolCalls takes a single integer argument specifying the
// maximum consecutive failure count.
//
// Example:
//
//	DefaultCaps(MaxConsecutiveFailedToolCalls(3))
func MaxConsecutiveFailedToolCalls(n int) CapsOption {
	return func(c *expragents.CapsExpr) {
		if n <= 0 {
			eval.ReportError("MaxConsecutiveFailedToolCalls requires n > 0, got %d", n)
			return
		}
		c.MaxConsecutiveFailedToolCall = n
	}
}
