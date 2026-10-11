// Package overflow supplies the pure pre-send decision for one planner step.
package overflow

import (
	"math"
	"math/bits"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
)

// Step carries guard eligibility and provider rejections across an overflow retry.
// ProviderLeft starts at two; Rejected stores the smallest whole-request Size
// rejected by each model in this step. Check never mutates this state.
type Step struct {
	GuardUsed, GuardOff bool
	ProviderLeft        int
	Rejected            map[string]int
}

// Limits supplies the input budget B and its nonnegative rational guard margin.
// A nonpositive Budget disables the guard. A zero margin means zero; callers
// supply Rate{Tokens: 1, Bytes: 10} for the default ten-percent margin.
type Limits struct {
	Budget      int
	GuardMargin contextmeasure.Rate
}

// Action is the pre-send decision, independent of provider I/O or recovery.
type Action uint8

const (
	// Send permits a provider request.
	Send Action = iota
	// GuardReject asks for advisory recovery without spending the provider budget.
	GuardReject
	// NoProgress refuses a request no smaller than this model already rejected.
	NoProgress
)

// Size measures input plus reserve at the uncalibrated default rate, so usage
// reported by an overflow cannot change the progress comparison.
func Size(f contextmeasure.RequestFacts) int {
	input := contextmeasure.Estimate(contextmeasure.Rate{}, 0, f.TextBytes, f.NonText)
	reserve := max(0, f.OutputReserve)
	if input > math.MaxInt-reserve {
		return math.MaxInt
	}
	return input + reserve
}

// Check evaluates guard rejection first, then the per-model provider baseline.
// It performs no state transitions and never spends ProviderLeft.
func Check(step Step, facts contextmeasure.RequestFacts, guard contextmeasure.GuardCount, limits Limits) Action {
	if guard.GuardOn && !step.GuardUsed && !step.GuardOff && limits.Budget > 0 && aboveMargin(guard.Tokens, limits) {
		return GuardReject
	}
	if rejected, exists := step.Rejected[facts.Model]; exists && Size(facts) >= rejected {
		return NoProgress
	}
	return Send
}

// aboveMargin compares (tokens-B)*denominator > B*numerator. Subtracting
// B first avoids adding the margin to its denominator; double-width products
// preserve the strict rational boundary even at MaxInt.
func aboveMargin(tokens int, limits Limits) bool {
	if tokens <= limits.Budget {
		return false
	}
	margin := limits.GuardMargin
	if margin.Tokens <= 0 || margin.Bytes <= 0 {
		return true
	}
	leftHi, leftLo := bits.Mul64(uint64(max(tokens-limits.Budget, 0)), uint64(margin.Bytes))
	rightHi, rightLo := bits.Mul64(uint64(max(limits.Budget, 0)), uint64(margin.Tokens))
	return leftHi > rightHi || (leftHi == rightHi && leftLo > rightLo)
}
