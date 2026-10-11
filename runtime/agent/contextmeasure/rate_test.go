package contextmeasure

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalibrate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		facts      RequestFacts
		rate       Rate
		calibrated bool
	}{
		{"above", RequestFacts{InputTokens: 90, TextBytes: 100}, Rate{1, 2}, true},
		{"below", RequestFacts{InputTokens: 1, TextBytes: 100}, Rate{1, 6}, true},
		{"nontext_exceeds_usage", RequestFacts{InputTokens: 10, NonText: 30, TextBytes: 100}, Rate{1, 6}, true},
		{"interior", RequestFacts{InputTokens: 80, NonText: 40, TextBytes: 120}, Rate{1, 3}, true},
		{"no_text", RequestFacts{InputTokens: 100}, Rate{1, 3}, false},
		{"no_usage", RequestFacts{TextBytes: 100}, Rate{1, 3}, false},
		{"upper_boundary", RequestFacts{InputTokens: 50, TextBytes: 100}, Rate{1, 2}, true},
		{"lower_boundary", RequestFacts{InputTokens: 10, TextBytes: 60}, Rate{1, 6}, true},
		{"large_ratio", RequestFacts{InputTokens: math.MaxInt / 3, TextBytes: math.MaxInt}, Rate{math.MaxInt / 3, math.MaxInt}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rate, calibrated := Calibrate(tc.facts)
			assert.Equal(t, tc.calibrated, calibrated)
			assert.True(t, equalRate(rate, tc.rate), "got %+v; want %+v", rate, tc.rate)
		})
	}
}

func TestEstimateCeilingAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		rate                       Rate
		fixed, view, nontext, want int
	}{
		{"ceiling", Rate{1, 3}, 1, 3, 2, 4},
		{"round_combined_once", Rate{1, 3}, 1, 1, 0, 1},
		{"negative_sizes", Rate{1, 3}, -10, -20, -30, 0},
		{"zero_rate_defaults", Rate{}, 3, 3, 0, 2},
		{"sum_exceeds_int", Rate{1, 2}, math.MaxInt, math.MaxInt, 0, math.MaxInt},
		{"product_exceeds_int", Rate{math.MaxInt, math.MaxInt}, math.MaxInt, 0, 0, math.MaxInt},
		{"saturate", Rate{1, 2}, math.MaxInt, math.MaxInt, 1, math.MaxInt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Estimate(tc.rate, tc.fixed, tc.view, tc.nontext))
		})
	}
}

func TestArithmeticProperties(t *testing.T) {
	random := rand.NewPCG(10, 2)
	for i := 0; i < 10000; i++ {
		facts := RequestFacts{InputTokens: randomInt(random), TextBytes: randomInt(random), NonText: randomInt(random)}
		rate, calibrated := Calibrate(facts)
		require.Positive(t, rate.Tokens)
		require.Positive(t, rate.Bytes)
		assert.True(t, rateWithinBounds(rate), "unbounded rate %+v for %+v", rate, facts)
		if !calibrated {
			assert.True(t, equalRate(rate, Rate{1, 3}), "uncalibrated rate %+v", rate)
		}
		fixed, view, nontext := randomInt(random), randomInt(random), randomInt(random)
		got := Estimate(rate, fixed, view, nontext)
		want := referenceEstimate(rate, fixed, view, nontext)
		assert.GreaterOrEqual(t, got, 0)
		assert.Equal(t, want, got, "rate %+v, sizes %d/%d/%d", rate, fixed, view, nontext)
	}
}

func TestOutputReserveAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		limits                     model.ContextLimits
		maxTokens, reserve, budget int
		known                      bool
	}{
		{"explicit", model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 400, DefaultMaxTokens: 200}, 300, 300, 700, true},
		{"configured", model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 400, DefaultMaxTokens: 200}, 0, 200, 800, true},
		{"quarter", model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 400}, 0, 250, 750, true},
		{"output_cap", model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100}, 0, 100, 900, true},
		{"unknown", model.ContextLimits{}, 100, 100, 0, false},
		{"exhausted", model.ContextLimits{ContextWindow: 100}, 100, 100, 0, false},
		{"negative_budget", model.ContextLimits{ContextWindow: 100}, 200, 200, 0, false},
		{"unknown_output", model.ContextLimits{ContextWindow: 100}, 0, 0, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reserve := OutputReserve(&model.Request{MaxTokens: tc.maxTokens}, tc.limits)
			budget, known := Budget(tc.limits, reserve)
			assert.Equal(t, tc.reserve, reserve)
			assert.Equal(t, tc.budget, budget)
			assert.Equal(t, tc.known, known)
		})
	}
}

func equalRate(a, b Rate) bool {
	left := new(big.Int).Mul(big.NewInt(int64(a.Tokens)), big.NewInt(int64(b.Bytes)))
	right := new(big.Int).Mul(big.NewInt(int64(b.Tokens)), big.NewInt(int64(a.Bytes)))
	return left.Cmp(right) == 0
}

func rateWithinBounds(rate Rate) bool {
	tokens, size := big.NewInt(int64(rate.Tokens)), big.NewInt(int64(rate.Bytes))
	return new(big.Int).Mul(tokens, big.NewInt(6)).Cmp(size) >= 0 && new(big.Int).Mul(tokens, big.NewInt(2)).Cmp(size) <= 0
}

func referenceEstimate(rate Rate, fixed, view, nontext int) int {
	size := new(big.Int).Add(big.NewInt(int64(max(fixed, 0))), big.NewInt(int64(max(view, 0))))
	total := new(big.Int).Mul(size, big.NewInt(int64(rate.Tokens)))
	total.Add(total, big.NewInt(int64(rate.Bytes-1)))
	total.Div(total, big.NewInt(int64(rate.Bytes)))
	total.Add(total, big.NewInt(int64(max(nontext, 0))))
	if total.Cmp(big.NewInt(int64(math.MaxInt))) > 0 {
		return math.MaxInt
	}
	return int(total.Int64())
}

func randomInt(source *rand.PCG) int {
	return int(source.Uint64() & uint64(math.MaxInt))
}
