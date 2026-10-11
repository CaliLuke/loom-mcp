package overflow_test

import (
	"fmt"
	"maps"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
)

func TestCheck(t *testing.T) {
	facts := contextmeasure.RequestFacts{Model: "m", TextBytes: 300, NonText: 10, OutputReserve: 20}
	for _, tc := range []struct {
		name   string
		step   overflow.Step
		guard  contextmeasure.GuardCount
		limits overflow.Limits
		want   overflow.Action
	}{
		{"guard_first", overflow.Step{Rejected: map[string]int{"m": 130}}, contextmeasure.GuardCount{Tokens: 111, GuardOn: true}, overflow.Limits{Budget: 100, GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}}, overflow.GuardReject},
		{"at_margin", overflow.Step{}, contextmeasure.GuardCount{Tokens: 110, GuardOn: true}, overflow.Limits{Budget: 100, GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}}, overflow.Send},
		{"fractional_margin", overflow.Step{}, contextmeasure.GuardCount{Tokens: 2, GuardOn: true}, overflow.Limits{Budget: 1, GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}}, overflow.GuardReject},
		{"guard_used", overflow.Step{GuardUsed: true}, contextmeasure.GuardCount{Tokens: 111, GuardOn: true}, overflow.Limits{Budget: 100}, overflow.Send},
		{"guard_off", overflow.Step{GuardOff: true}, contextmeasure.GuardCount{Tokens: 111, GuardOn: true}, overflow.Limits{Budget: 100}, overflow.Send},
		{"uncalibrated", overflow.Step{}, contextmeasure.GuardCount{Tokens: 111}, overflow.Limits{Budget: 100}, overflow.Send},
		{"unknown_limits", overflow.Step{}, contextmeasure.GuardCount{Tokens: 111, GuardOn: true}, overflow.Limits{}, overflow.Send},
		{"equal_provider_size", overflow.Step{Rejected: map[string]int{"m": 130}}, contextmeasure.GuardCount{}, overflow.Limits{}, overflow.NoProgress},
		{"larger_provider_size", overflow.Step{Rejected: map[string]int{"m": 129}}, contextmeasure.GuardCount{}, overflow.Limits{}, overflow.NoProgress},
		{"smaller_provider_size", overflow.Step{Rejected: map[string]int{"m": 131}}, contextmeasure.GuardCount{Tokens: 999}, overflow.Limits{}, overflow.Send},
		{"other_model", overflow.Step{Rejected: map[string]int{"other": 1}}, contextmeasure.GuardCount{}, overflow.Limits{}, overflow.Send},
		{"zero_rejection", overflow.Step{Rejected: map[string]int{"m": 0}}, contextmeasure.GuardCount{}, overflow.Limits{}, overflow.NoProgress},
		{"large_margin", overflow.Step{}, contextmeasure.GuardCount{Tokens: math.MaxInt, GuardOn: true}, overflow.Limits{Budget: math.MaxInt, GuardMargin: contextmeasure.Rate{Tokens: math.MaxInt, Bytes: math.MaxInt}}, overflow.Send},
		{"large_products", overflow.Step{}, contextmeasure.GuardCount{Tokens: math.MaxInt, GuardOn: true}, overflow.Limits{Budget: math.MaxInt / 2, GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: math.MaxInt}}, overflow.GuardReject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.step
			before.Rejected = maps.Clone(tc.step.Rejected)
			assert.Equal(t, tc.want, overflow.Check(tc.step, facts, tc.guard, tc.limits))
			assert.Equal(t, before, tc.step, "Check must not mutate budgets or rejections")
		})
	}
}

func TestCheckPerModelWholeRequestProgress(t *testing.T) {
	step := overflow.Step{ProviderLeft: 1, Rejected: map[string]int{"small": 10, "large": 100}}
	for _, tc := range []struct {
		model              string
		viewBytes, reserve int
		want               overflow.Action
	}{
		{"small", 30, 0, overflow.NoProgress},
		{"small", 3, 0, overflow.NoProgress}, // Shrinking only the view is insufficient.
		{"large", 3, 0, overflow.Send},
		{"new", 3, 0, overflow.Send},
		{"large", 3, 90, overflow.NoProgress}, // Output reserve is part of Size.
	} {
		t.Run(fmt.Sprintf("%s/view=%d/reserve=%d", tc.model, tc.viewBytes, tc.reserve), func(t *testing.T) {
			facts := contextmeasure.RequestFacts{Model: tc.model, TextBytes: 30, ViewTextBytes: tc.viewBytes, OutputReserve: tc.reserve}
			assert.Equal(t, tc.want, overflow.Check(step, facts, contextmeasure.GuardCount{}, overflow.Limits{}))
		})
	}
}

func TestSizeUsesUncalibratedWholeRequest(t *testing.T) {
	facts := contextmeasure.RequestFacts{TextBytes: 7, FixedBytes: 3, NonText: 2, OutputReserve: 4, ViewTextBytes: 1}
	assert.Equal(t, 9, overflow.Size(facts))
	facts.InputTokens = 1000 // Overflow usage may calibrate, but must not change Size.
	assert.Equal(t, 9, overflow.Size(facts))
	facts.TextBytes, facts.NonText, facts.OutputReserve = math.MaxInt, math.MaxInt, math.MaxInt
	assert.Equal(t, math.MaxInt, overflow.Size(facts))
}
