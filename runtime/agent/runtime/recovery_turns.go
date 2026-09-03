package runtime

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// consumeRecoveryTurn reserves one replacement planner activity.
func consumeRecoveryTurn(caps *policy.CapsState) bool {
	if caps == nil || caps.RemainingRecoveryTurns <= 0 {
		return false
	}
	caps.RemainingRecoveryTurns--
	return true
}

// resetRecoveryTurnsAfterResults starts a new recovery episode only after a
// successful registered domain tool completed. Runtime recovery tools and
// failed work never reset the allowance.
func resetRecoveryTurnsAfterResults(r *Runtime, caps *policy.CapsState, results []*planner.ToolResult) {
	if r == nil || caps == nil || caps.MaxRecoveryTurns == 0 {
		return
	}
	for _, result := range results {
		if result == nil || result.Error != nil || result.Name == tools.ToolUnavailable {
			continue
		}
		if spec, ok := r.toolSpec(result.Name); ok && !spec.Bookkeeping {
			caps.RemainingRecoveryTurns = caps.MaxRecoveryTurns
			return
		}
	}
}

// resultsRequireRecovery reports whether the next planner activity replaces
// rejected tool output rather than advancing successful domain work.
func resultsRequireRecovery(results []*planner.ToolResult) bool {
	for _, result := range results {
		if result == nil || result.Error == nil {
			continue
		}
		if result.RetryHint == nil {
			return true
		}
		switch result.RetryHint.Reason {
		case planner.RetryReasonInvalidArguments,
			planner.RetryReasonMissingFields,
			planner.RetryReasonMalformedResponse,
			planner.RetryReasonToolUnavailable,
			planner.RetryReasonUnsupportedOperation:
			return true
		case planner.RetryReasonTimeout,
			planner.RetryReasonRateLimited:
			continue
		default:
			continue
		}
	}
	return false
}
