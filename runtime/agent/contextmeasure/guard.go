package contextmeasure

import (
	"context"
	"math"
	"math/bits"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// GuardBudget carries the input budget and the adapter's non-text image cost.
// A nonpositive Tokens value represents unknown or unusable limits.
type GuardBudget struct {
	Tokens, ImageTokens int
}

// GuardCount reports measurement only, without rejecting a provider request.
type GuardCount struct {
	Tokens                  int
	Exact, Counted, GuardOn bool
	Latency                 time.Duration
}

// GuardTokens counts only requests above the rational trigger threshold, trusts
// exact counts only, and adds thinking excluded by the provider counter.
// A zero triggerAt disables counting; the estimate and calibrated GuardOn still
// apply. triggerAt is a threshold fraction, not an estimation rate default.
func GuardTokens(ctx context.Context, counter model.TokenCounter, req *model.Request, rate Rate, calibrated bool, budget GuardBudget, triggerAt Rate) GuardCount {
	facts, err := Measure(req, model.ContextLimits{ImageTokens: budget.ImageTokens}, nil)
	if err != nil {
		// Invalid request surfaces cannot produce a usable guard measurement.
		return GuardCount{Tokens: math.MaxInt}
	}
	known := budget.Tokens > 0
	result := GuardCount{
		Tokens:  Estimate(rate, facts.FixedBytes, facts.TextBytes-facts.FixedBytes, facts.NonText),
		GuardOn: known && calibrated,
	}
	if !known || counter == nil || !aboveTrigger(result.Tokens, budget.Tokens, triggerAt) {
		return result
	}
	start := time.Now()
	count, err := counter.CountTokens(ctx, req)
	result.Latency = time.Since(start)
	result.Counted = true
	if err != nil || !count.Exact || count.InputTokens < 0 {
		return result
	}
	thinking, err := thinkingBytes(req)
	if err != nil {
		return result
	}
	result.Tokens = addSize(count.InputTokens, Estimate(rate, 0, thinking, 0))
	result.Exact = true
	result.GuardOn = true
	return result
}

// aboveTrigger compares double-width integer products, preserving a fractional
// threshold without rounding it up or overflowing either multiplication.
func aboveTrigger(tokens, budget int, trigger Rate) bool {
	if trigger.Tokens < 0 || trigger.Bytes <= 0 {
		return false
	}
	leftHi, leftLo := bits.Mul64(uint64(max(tokens, 0)), uint64(trigger.Bytes))
	rightHi, rightLo := bits.Mul64(uint64(max(budget, 0)), uint64(trigger.Tokens))
	return leftHi > rightHi || (leftHi == rightHi && leftLo > rightLo)
}

func thinkingBytes(req *model.Request) (int, error) {
	total := 0
	for _, message := range req.Messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			normalized, err := normalizePart(part)
			if err != nil {
				return 0, err
			}
			if thinking, ok := normalized.(model.ThinkingPart); ok {
				total = addSize(total, addSize(len(thinking.Text), len(thinking.Redacted)))
			}
		}
	}
	return total, nil
}
