package contextmeasure

import (
	"math"
	"math/bits"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Rate expresses an integer ratio. Estimate uses it as tokens per byte, with
// zero selecting 1/3. GuardTokens uses triggerAt as a threshold fraction, where
// zero disables counting; it does not select the estimation default.
type Rate struct {
	Tokens, Bytes int
}

// Calibrate derives a rate clamped to [1/6, 1/2]. Missing usage or text uses 1/3.
func Calibrate(f RequestFacts) (Rate, bool) {
	if f.InputTokens <= 0 || f.TextBytes <= 0 {
		return Rate{1, 3}, false
	}
	tokens := f.InputTokens - min(f.InputTokens, max(f.NonText, 0))
	if tokens < (f.TextBytes-1)/6+1 {
		return Rate{1, 6}, true
	}
	if tokens > f.TextBytes/2 {
		return Rate{1, 2}, true
	}
	divisor := gcd(tokens, f.TextBytes)
	return Rate{tokens / divisor, f.TextBytes / divisor}, true
}

// Estimate rounds the combined text cost up and adds non-text tokens. Negative
// sizes contribute zero; unrepresentable results saturate at math.MaxInt.
func Estimate(rate Rate, fixed, viewText, nonText int) int {
	size := uint64(max(fixed, 0)) + uint64(max(viewText, 0))
	return addSize(ceilTokens(rate, size), nonText)
}

// OutputReserve chooses explicit, configured, then bounded fallback output tokens.
func OutputReserve(req *model.Request, limits model.ContextLimits) int {
	if req != nil && req.MaxTokens > 0 {
		return req.MaxTokens
	}
	if limits.DefaultMaxTokens > 0 {
		return limits.DefaultMaxTokens
	}
	return min(max(limits.MaxOutputTokens, 0), max(limits.ContextWindow, 0)/4)
}

// Budget reports a positive input budget when the context window is known.
func Budget(limits model.ContextLimits, reserve int) (int, bool) {
	if limits.ContextWindow <= 0 || reserve >= limits.ContextWindow {
		return 0, false
	}
	return limits.ContextWindow - max(reserve, 0), true
}

// ceilTokens uses a double-width product so multiplication cannot wrap before
// division. The sum of two nonnegative ints fits in a uint64 on supported Go targets.
func ceilTokens(rate Rate, size uint64) int {
	if rate.Tokens <= 0 || rate.Bytes <= 0 {
		rate = Rate{1, 3}
	}
	hi, lo := bits.Mul64(size, uint64(max(rate.Tokens, 0)))
	divisor := uint64(max(rate.Bytes, 0))
	if hi >= divisor {
		return math.MaxInt
	}
	quotient, remainder := bits.Div64(hi, lo, divisor)
	if quotient >= uint64(math.MaxInt) {
		return math.MaxInt
	}
	if remainder != 0 {
		quotient++
	}
	return int(quotient)
}

// addSize sums nonnegative measurements without wrapping an int.
func addSize(a, b int) int {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
