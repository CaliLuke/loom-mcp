package contextlimits

import (
	"math"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ForRequest maps cached input-only metadata to a shared window for this request.
// Subtracting OutputReserve from the returned window preserves InputLimit.
// Table fallback windows are already shared and remain unchanged.
func (r Result) ForRequest(req *model.Request) (model.ContextLimits, bool) {
	limits := r.Limits
	if r.InputLimit <= 0 {
		return limits, r.Known
	}
	limits.ContextWindow = r.InputLimit
	if (req == nil || req.MaxTokens <= 0) && limits.DefaultMaxTokens <= 0 {
		// Solve reserve = min(maxOutput, (input + reserve)/4), choosing
		// min(maxOutput, input/3). Re-evaluation on the returned window is stable.
		reserve := min(max(limits.MaxOutputTokens, 0), r.InputLimit/3)
		if r.InputLimit > math.MaxInt-reserve {
			limits.ContextWindow = 0
			return limits, false
		}
		limits.ContextWindow += reserve
	}
	reserve := contextmeasure.OutputReserve(req, limits)
	if r.InputLimit > math.MaxInt-reserve {
		limits.ContextWindow = 0
		return limits, false
	}
	limits.ContextWindow = r.InputLimit + reserve
	return limits, true
}
