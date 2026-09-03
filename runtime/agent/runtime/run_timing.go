// Package runtime centralizes the active-work deadline contract used by workflow
// execution.
package runtime

import "time"

type runTiming struct {
	TimeBudget     time.Duration
	FinalizerGrace time.Duration
}

// resolveRunTiming derives the workflow timing contract used by ExecuteWorkflow.
// TimeBudget governs active planner/tool execution; zero leaves it unlimited.
// FinalizerGrace always reserves enough time for one final planner resume turn.
func resolveRunTiming(reg AgentRegistration, input *RunInput) runTiming {
	var timing runTiming
	if reg.Policy.TimeBudget > 0 {
		timing.TimeBudget = reg.Policy.TimeBudget
	}
	if input != nil && input.Policy != nil && input.Policy.TimeBudget > 0 {
		timing.TimeBudget = input.Policy.TimeBudget
	}

	switch {
	case input != nil && input.Policy != nil && input.Policy.FinalizerGrace > 0:
		timing.FinalizerGrace = input.Policy.FinalizerGrace
	case reg.Policy.FinalizerGrace > 0:
		timing.FinalizerGrace = reg.Policy.FinalizerGrace
	default:
		timing.FinalizerGrace = defaultFinalizerGrace
	}

	resumeTimeout := reg.ResumeActivityOptions.StartToCloseTimeout
	if input != nil && input.Policy != nil && input.Policy.PlanTimeout > 0 {
		resumeTimeout = input.Policy.PlanTimeout
	}
	if resumeTimeout == 0 {
		resumeTimeout = defaultResumeActivityTimeout
	}
	if timing.FinalizerGrace < resumeTimeout {
		timing.FinalizerGrace = resumeTimeout
	}
	return timing
}
