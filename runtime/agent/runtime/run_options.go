package runtime

import (
	"maps"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// MissingFieldsAction controls behavior when a tool validation error indicates
// missing fields. It is string-backed for JSON friendliness. Empty value means
// unspecified (planner decides).
type MissingFieldsAction string

const (
	// MissingFieldsFinalize instructs the runtime to finalize immediately
	// when fields are missing.
	MissingFieldsFinalize MissingFieldsAction = "finalize"
	// MissingFieldsAwaitClarification instructs the runtime to pause and await user clarification.
	MissingFieldsAwaitClarification MissingFieldsAction = "await_clarification"
	// MissingFieldsResume instructs the runtime to continue without pausing; surface hints to the planner.
	MissingFieldsResume MissingFieldsAction = "resume"
)

// RunOption configures optional fields on RunInput for Run and Start. Required
// values such as SessionID are positional arguments on AgentClient methods and
// must not be set via RunOption.
type RunOption func(*RunInput)

// WithRunID sets the RunID on the constructed RunInput.
func WithRunID(id string) RunOption {
	return func(in *RunInput) { in.RunID = id }
}

// WithLabels merges the provided labels into the constructed RunInput.
func WithLabels(labels map[string]string) RunOption {
	return func(in *RunInput) { in.Labels = mergeLabels(in.Labels, labels) }
}

// WithTurnID sets the TurnID on the constructed RunInput.
func WithTurnID(id string) RunOption {
	return func(in *RunInput) { in.TurnID = id }
}

// WithMetadata merges the provided metadata into the constructed RunInput.
func WithMetadata(meta map[string]any) RunOption {
	return func(in *RunInput) {
		if len(meta) == 0 {
			return
		}
		if in.Metadata == nil {
			in.Metadata = make(map[string]any, len(meta))
		}
		for k, v := range meta {
			in.Metadata[k] = v
		}
	}
}

// WithTaskQueue sets the target task queue on WorkflowOptions for this run.
func WithTaskQueue(name string) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		in.WorkflowOptions.TaskQueue = name
	}
}

// WithMemo sets memo on WorkflowOptions for this run.
func WithMemo(m map[string]any) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		if in.WorkflowOptions.Memo == nil {
			in.WorkflowOptions.Memo = make(map[string]any, len(m))
		}
		for k, v := range m {
			in.WorkflowOptions.Memo[k] = v
		}
	}
}

// WithSearchAttributes sets search attributes on WorkflowOptions for this run.
func WithSearchAttributes(sa map[string]any) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		if in.WorkflowOptions.SearchAttributes == nil {
			in.WorkflowOptions.SearchAttributes = make(map[string]any, len(sa))
		}
		maps.Copy(in.WorkflowOptions.SearchAttributes, sa)
	}
}

// WithWorkflowOptions sets workflow engine options on the constructed RunInput.
func WithWorkflowOptions(o *WorkflowOptions) RunOption {
	return func(in *RunInput) { in.WorkflowOptions = o }
}

// WithTiming sets run-level timing overrides in a single structured option.
// Budget is the semantic run budget; Plan and Tools are attempt budgets. Zero-
// valued fields are ignored.
func WithTiming(t Timing) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		if t.Budget > 0 {
			in.Policy.TimeBudget = t.Budget
		}
		if t.Plan > 0 {
			in.Policy.PlanTimeout = t.Plan
		}
		if t.Tools > 0 {
			in.Policy.ToolTimeout = t.Tools
		}
		if len(t.PerToolTimeout) > 0 {
			if in.Policy.PerToolTimeout == nil {
				in.Policy.PerToolTimeout = make(map[tools.Ident]time.Duration, len(t.PerToolTimeout))
			}
			for k, v := range t.PerToolTimeout {
				in.Policy.PerToolTimeout[k] = v
			}
		}
	}
}

// WithPerTurnMaxToolCalls sets a per-turn cap on tool executions. Zero means unlimited.
func WithPerTurnMaxToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.PerTurnMaxToolCalls = n
	}
}

// WithRunMaxToolCalls sets a per-run cap on total tool executions.
func WithRunMaxToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.MaxToolCalls = n
	}
}

// WithRunMaxConsecutiveFailedToolCalls caps consecutive failures before aborting the run.
func WithRunMaxConsecutiveFailedToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.MaxConsecutiveFailedToolCalls = n
	}
}

// WithRunTimeBudget sets the semantic wall-clock budget for planner and tool work in the run.
func WithRunTimeBudget(d time.Duration) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.TimeBudget = d
	}
}

// WithRunFinalizerGrace reserves time to produce a final assistant message after the run budget is exhausted.
func WithRunFinalizerGrace(d time.Duration) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.FinalizerGrace = d
	}
}

// WithRunInterruptsAllowed enables human-in-the-loop interruptions for this run.
func WithRunInterruptsAllowed(allowed bool) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.InterruptsAllowed = allowed
	}
}

// WithRestrictToTool restricts candidate tools to a single tool for the run.
func WithRestrictToTool(id tools.Ident) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.RestrictToTool = id
	}
}

// WithAllowedTags filters candidate tools to those whose tags intersect this list.
func WithAllowedTags(tags []string) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.AllowedTags = append([]string(nil), tags...)
	}
}

// WithDeniedTags filters out candidate tools that have any of these tags.
func WithDeniedTags(tags []string) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.DeniedTags = append([]string(nil), tags...)
	}
}

// defaultRetriedActivityPolicy returns the runtime's standard infrastructure
// retry policy for activities whose logical work is now replay-safe by
// contract. Planner/tool business errors still surface in typed results rather
// than escaping as activity failures.
func defaultRetriedActivityPolicy() engine.RetryPolicy {
	return engine.RetryPolicy{
		MaxAttempts:        3,
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
	}
}
