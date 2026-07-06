package agent

import (
	"fmt"
	"time"

	"github.com/CaliLuke/loom/eval"
)

type (
	// RunPolicyExpr defines runtime execution and resource constraints for a
	// single agent.
	RunPolicyExpr struct {
		eval.DSLFunc

		// Agent is the agent expression this policy applies to.
		Agent *AgentExpr
		// DefaultCaps specifies default per-run limits on tool usage.
		DefaultCaps *CapsExpr
		// TimeBudget is the maximum duration a run may execute before
		// being terminated.
		TimeBudget time.Duration
		// PlanTimeout applies to both Plan and Resume activities when set.
		PlanTimeout time.Duration
		// ToolTimeout is the default ExecuteTool activity timeout when set.
		ToolTimeout time.Duration
		// InterruptsAllowed indicates whether the agent can be
		// interrupted during execution.
		InterruptsAllowed bool
		// OnMissingFields controls behavior when validation indicates
		// missing fields.  Allowed values: "finalize" |
		// "await_clarification" | "resume". Empty means unspecified.
		OnMissingFields string
		// History configures how the runtime prunes or compresses
		// conversational history before planner invocations.
		History *HistoryExpr
		// Cache configures prompt caching hints for planner/model calls.
		Cache *CacheExpr
		// RetryAndReflect configures tool-error reflection into planner retry
		// hints for this agent.
		RetryAndReflect *RetryAndReflectExpr
		// Interceptors lists application-owned interceptor IDs for this agent.
		Interceptors []string
		// PreloadMemory configures bounded planner-input memory preload.
		PreloadMemory *MemoryPreloadExpr
	}

	// MemoryScope identifies the memory source used by preload policy.
	MemoryScope string

	// MemoryPreloadExpr captures design-time memory preload policy.
	MemoryPreloadExpr struct {
		eval.DSLFunc

		// Policy is the run policy expression this preload configuration belongs to.
		Policy *RunPolicyExpr
		// Scope selects the memory source.
		Scope MemoryScope
		// MaxResults caps the number of memory events injected into planner input.
		MaxResults int
	}

	// RetryAndReflectExpr captures tool retry reflection policy for an agent.
	RetryAndReflectExpr struct {
		// Policy is the run policy expression this retry configuration belongs to.
		Policy *RunPolicyExpr
		// MaxRetries bounds reflected retry hints per run/tool. Zero uses the
		// runtime default.
		MaxRetries int
		// ErrorIfRetryExceeded returns the original tool error after the retry
		// budget has been consumed.
		ErrorIfRetryExceeded bool
	}

	// CapsExpr defines per-run limits on agent tool usage.
	CapsExpr struct {
		// Policy is the run policy expression this caps configuration
		// belongs to.
		Policy *RunPolicyExpr
		// MaxToolCalls is the maximum number of tool invocations
		// allowed in a single run.
		MaxToolCalls int
		// MaxConsecutiveFailedToolCall is the maximum number of
		// consecutive tool failures before the run is terminated.
		MaxConsecutiveFailedToolCall int
	}

	// HistoryMode identifies which history policy is configured on an agent.
	HistoryMode string

	// HistoryExpr captures the design-time configuration for history
	// management. It encodes either a KeepRecentTurns or Compress
	// policy; at most one mode may be set.
	HistoryExpr struct {
		eval.DSLFunc

		// Policy is the run policy expression this history configuration
		// belongs to.
		Policy *RunPolicyExpr
		// Mode selects the history strategy.
		Mode HistoryMode
		// KeepRecent is the number of recent turns to retain when
		// ModeKeepRecent is selected.
		KeepRecent int
		// CompressAtTurns is the optional logical-turn threshold that triggers
		// compression.
		CompressAtTurns int
		// CompressAtMaxInputTokens is the optional runtime-counted input-token
		// threshold that triggers compression.
		CompressAtMaxInputTokens int
		// KeepMaxTurns is the optional maximum number of newest logical turns to
		// retain exactly after compression.
		KeepMaxTurns int
		// KeepMaxInputTokens is the optional runtime-counted input-token budget
		// for newest exact turns after compression.
		KeepMaxInputTokens int
	}

	// CacheExpr captures the design-time configuration for prompt caching
	// behavior. Zero-value means no cache policy is configured.
	CacheExpr struct {
		eval.DSLFunc

		// Policy is the run policy expression this cache configuration
		// belongs to.
		Policy *RunPolicyExpr
		// AfterSystem places a cache checkpoint after all system messages.
		AfterSystem bool
		// AfterTools places a cache checkpoint after tool definitions.
		AfterTools bool
	}
)

const (
	// HistoryModeKeepRecent configures a sliding-window policy that
	// retains only the most recent N turns.
	HistoryModeKeepRecent HistoryMode = "keep_recent"
	// HistoryModeCompress configures a summarization policy that
	// compresses older turns once a trigger threshold is reached.
	HistoryModeCompress HistoryMode = "compress"
	// MemoryScopeCurrentRun scopes memory preload to the current run snapshot.
	MemoryScopeCurrentRun MemoryScope = "current_run"
	// MemoryScopeIndexed scopes memory preload to the configured memory searcher.
	MemoryScopeIndexed MemoryScope = "indexed"
)

// EvalName returns a descriptive identifier for error reporting.
func (r *RunPolicyExpr) EvalName() string {
	return fmt.Sprintf("run policy for agent %q", r.Agent.Name)
}

// Validate enforces semantic constraints on the run policy.
func (r *RunPolicyExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	r.validateMissingFields(verr)
	r.validateHistory(verr)
	r.validateRetryAndReflect(verr)
	r.validateInterceptors(verr)
	r.validatePreloadMemory(verr)
	if len(verr.Errors) == 0 {
		return nil
	}
	return verr
}

func (r *RunPolicyExpr) validateInterceptors(verr *eval.ValidationErrors) {
	seen := make(map[string]struct{}, len(r.Interceptors))
	for _, id := range r.Interceptors {
		if id == "" {
			verr.Add(r, "interceptor id must be non-empty")
			continue
		}
		if _, ok := seen[id]; ok {
			verr.Add(r, "duplicate interceptor id %q", id)
			continue
		}
		seen[id] = struct{}{}
	}
}

// validateMissingFields checks the missing-field policy cross-field contract.
func (r *RunPolicyExpr) validateMissingFields(verr *eval.ValidationErrors) {
	if r.OnMissingFields != "" {
		switch r.OnMissingFields {
		case "finalize", "await_clarification", "resume":
			// ok
		default:
			verr.Add(r, "invalid OnMissingFields value %q (allowed: finalize, await_clarification, resume)", r.OnMissingFields)
		}
		if r.OnMissingFields == "await_clarification" && !r.InterruptsAllowed {
			verr.Add(r, "OnMissingFields(\"await_clarification\") requires InterruptsAllowed(true)")
		}
	}
}

func (r *RunPolicyExpr) validateRetryAndReflect(verr *eval.ValidationErrors) {
	if r.RetryAndReflect == nil {
		return
	}
	if r.RetryAndReflect.MaxRetries < 0 {
		verr.Add(r.RetryAndReflect, "RetryAndReflect MaxRetries must be non-negative")
	}
}

func (r *RunPolicyExpr) validatePreloadMemory(verr *eval.ValidationErrors) {
	if r.PreloadMemory == nil {
		return
	}
	switch r.PreloadMemory.Scope {
	case "":
		verr.Add(r.PreloadMemory, "PreloadMemory requires a scope")
	case MemoryScopeCurrentRun, MemoryScopeIndexed:
		// ok
	default:
		verr.Add(r.PreloadMemory, "unknown PreloadMemory scope %q", r.PreloadMemory.Scope)
	}
	if r.PreloadMemory.MaxResults < 0 {
		verr.Add(r.PreloadMemory, "PreloadMemory MaxResults must be non-negative")
	}
}

// validateHistory checks the selected history mode and its required settings.
func (r *RunPolicyExpr) validateHistory(verr *eval.ValidationErrors) {
	if r.History != nil {
		switch r.History.Mode {
		case "":
			verr.Add(r.History, "history policy must specify a mode")
		case HistoryModeKeepRecent:
			if r.History.KeepRecent <= 0 {
				verr.Add(r.History, "KeepRecentTurns requires a positive turn count")
			}
		case HistoryModeCompress:
			r.History.validateCompress(verr)
		default:
			verr.Add(r.History, "unknown history mode %q", r.History.Mode)
		}
	}
}

// validateCompress checks token-aware compression triggers and retention caps.
func (h *HistoryExpr) validateCompress(verr *eval.ValidationErrors) {
	if h.CompressAtTurns <= 0 && h.CompressAtMaxInputTokens <= 0 {
		verr.Add(h, "compression requires CompressAtTurns or CompressAtMaxInputTokens")
	}
	if h.KeepMaxTurns <= 0 && h.KeepMaxInputTokens <= 0 {
		verr.Add(h, "compression requires KeepMaxTurns or KeepMaxInputTokens")
	}
	if h.CompressAtTurns < 0 {
		verr.Add(h, "CompressAtTurns must be positive when set")
	}
	if h.CompressAtMaxInputTokens < 0 {
		verr.Add(h, "CompressAtMaxInputTokens must be positive when set")
	}
	if h.KeepMaxTurns < 0 {
		verr.Add(h, "KeepMaxTurns must be positive when set")
	}
	if h.KeepMaxInputTokens < 0 {
		verr.Add(h, "KeepMaxInputTokens must be positive when set")
	}
	if h.CompressAtTurns > 0 && h.KeepMaxTurns >= h.CompressAtTurns {
		verr.Add(h, "KeepMaxTurns must be less than CompressAtTurns")
	}
}

// EvalName returns a descriptive identifier for error reporting.
func (h *HistoryExpr) EvalName() string {
	if h == nil || h.Policy == nil || h.Policy.Agent == nil {
		return "history policy"
	}
	return fmt.Sprintf("history policy for agent %q", h.Policy.Agent.Name)
}

// EvalName returns a descriptive identifier for error reporting.
func (c *CacheExpr) EvalName() string {
	if c == nil || c.Policy == nil || c.Policy.Agent == nil {
		return "cache policy"
	}
	return fmt.Sprintf("cache policy for agent %q", c.Policy.Agent.Name)
}

// EvalName returns a descriptive identifier for error reporting.
func (c *CapsExpr) EvalName() string {
	return fmt.Sprintf("caps for agent %q", c.Policy.Agent.Name)
}

// EvalName returns a descriptive identifier for error reporting.
func (r *RetryAndReflectExpr) EvalName() string {
	if r == nil || r.Policy == nil || r.Policy.Agent == nil {
		return "retry and reflect policy"
	}
	return fmt.Sprintf("retry and reflect policy for agent %q", r.Policy.Agent.Name)
}

// EvalName returns a descriptive identifier for error reporting.
func (m *MemoryPreloadExpr) EvalName() string {
	if m == nil || m.Policy == nil || m.Policy.Agent == nil {
		return "memory preload policy"
	}
	return fmt.Sprintf("memory preload policy for agent %q", m.Policy.Agent.Name)
}
