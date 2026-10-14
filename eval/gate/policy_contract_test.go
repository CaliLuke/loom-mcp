package gate

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func proposedTestPolicy() *Policy {
	return &Policy{Version: "per-variant-v2", Success: -.05, Unsupported: map[string]float64{"receipt_only": .02, "hybrid": .03}, Evidence: -.05, Recall: .8, Guard: -.1, GuardMethod: "scenario-upper-bound", Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: Bootstrap{Resamples: 10000, Seed: 42}, BootstrapMethod: "stratified-item-percentile"}
}
func policyConfig() Config {
	c := testConfig()
	c.Policy = proposedTestPolicy()
	return c
}
func TestK19cContractExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing", func(c *Config) { c.Policy = nil }},
		{"alpha absent", func(c *Config) { c.Policy.Alpha = 0 }},
		{"alpha nonfinite", func(c *Config) { c.Policy.Alpha = math.NaN() }},
		{"unknown guard", func(c *Config) { c.Policy.GuardMethod = "point" }},
		{"unknown bootstrap", func(c *Config) { c.Policy.BootstrapMethod = "iid" }},
		{"unsupported margin", func(c *Config) { c.Policy.Unsupported["hybrid"] = .04 }},
		{"missing variant", func(c *Config) { delete(c.Policy.Unsupported, "hybrid") }},
		{"altered looks", func(c *Config) { c.Policy.Looks = []int{12, 25, 48} }},
		{"bootstrap count", func(c *Config) { c.Policy.Bootstrap.Resamples = 10 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := policyConfig()
			tc.change(&c)
			_, err := Evaluate(testTable(12), c, 1)
			require.Error(t, err)
		})
	}
}
func TestK19cContractIntervalGuard(t *testing.T) {
	rows := testTable(12)
	for i := range rows {
		if rows[i].Variant != "baseline" && rows[i].Scenario == "a" && slices.Contains([]string{"0", "1"}, rows[i].Item) {
			rows[i].Success = false
		}
	}
	e, err := Evaluate(rows, policyConfig(), 1)
	require.NoError(t, err)
	for _, v := range e.Variants {
		assert.Less(t, v.Success.Estimate, 0.0)
		interval, ok := v.ScenarioIntervals["a"]
		require.True(t, ok)
		assert.Less(t, interval.Estimate, -.1)
		assert.GreaterOrEqual(t, interval.Hi, -.1)
		assert.Empty(t, v.GuardFailures, "a noisy negative point is not a guard failure")
	}
}
func decisionEvidence(t *testing.T) Evaluation {
	t.Helper()
	e, err := Evaluate(testTable(48), policyConfig(), 1)
	require.NoError(t, err)
	return e
}
func TestK19cContractIndependentDecisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*VariantResult)
		want   Status
		state  Status
	}{
		{"pass", func(*VariantResult) {}, Pass, Complete},
		{"regression", func(v *VariantResult) { v.Success.Status = Fail }, Fail, Complete},
		{"precision", func(v *VariantResult) { v.Success.Status = Inconclusive }, Continue, Continue},
		{"missing", func(v *VariantResult) { v.Missing = []Key{{Variant: v.Variant, Scenario: "a", Item: "0", Repeat: 2}} }, Incomplete, Incomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := decisionEvidence(t)
			tc.change(&e.Variants[0])
			d, err := Decide(e, nil)
			require.NoError(t, err)
			require.Len(t, d.Variants, 2)
			assert.Equal(t, tc.want, d.Variants[0].Status)
			assert.Equal(t, Pass, d.Variants[1].Status)
			assert.Equal(t, tc.state, d.Status)
		})
	}
}
func TestK19cContractGuardEqualityAndModes(t *testing.T) {
	for _, hi := range []float64{-.11, -.10, -.09} {
		e := decisionEvidence(t)
		e.Variants[0].ScenarioIntervals = map[string]Interval{"a": {Estimate: -.2, Lo: -.3, Hi: hi}}
		d, err := Decide(e, nil)
		require.NoError(t, err)
		require.Len(t, d.Variants, 2)
		want := Pass
		if hi < -.10 {
			want = Fail
		}
		assert.Equal(t, want, d.Variants[0].Status)
	}
	e := decisionEvidence(t)
	e.Look = 2
	_, err := Decide(e, nil)
	require.Error(t, err, "operational look2 cannot omit prefix")
	_, err = DecideMode(e, nil, Offline)
	require.NoError(t, err)
	_, err = DecideMode(e, nil, DecisionMode("unknown"))
	require.Error(t, err)
}
func TestK19cContractTerminalSnapshotAndBarrier(t *testing.T) {
	e := decisionEvidence(t)
	e.Variants[0].Success.Status = Inconclusive
	first, err := Decide(e, nil)
	require.NoError(t, err)
	require.Len(t, first.Variants, 2)
	next := e
	next.Look = 2
	next.Variants = slices.Clone(e.Variants)
	next.Variants = next.Variants[:1]
	next.Variants[0].Success.Status = Pass
	second, err := Decide(next, []Decision{first})
	require.NoError(t, err)
	require.Len(t, second.Variants, 2)
	assert.Equal(t, first.Variants[1], second.Variants[1], "stopped sibling retains its exact look1 evidence")
	_, err = Decide(next, []Decision{second})
	require.Error(t, err)
	e.Variants[0].Missing = []Key{{Variant: "receipt_only", Scenario: "a", Item: "0", Repeat: 1}}
	incomplete, err := Decide(e, nil)
	require.NoError(t, err)
	_, err = Decide(next, []Decision{incomplete})
	require.Error(t, err, "global same-look barrier")
}

func TestK19cContractVariantLocalMissingAndBaseline(t *testing.T) {
	for _, variant := range []string{"receipt_only", "baseline"} {
		t.Run(variant, func(t *testing.T) {
			rows := slices.DeleteFunc(testTable(48), func(row Row) bool {
				return row.Variant == variant && row.Scenario == "a" && row.Item == "0" && row.Repeat == 2
			})
			e, err := Evaluate(rows, policyConfig(), 1)
			require.NoError(t, err)
			d, err := Decide(e, nil)
			require.NoError(t, err)
			require.Len(t, d.Variants, 2)
			assert.Equal(t, Incomplete, d.Variants[0].Status)
			want := Pass
			if variant == "baseline" {
				want = Incomplete
			}
			assert.Equal(t, want, d.Variants[1].Status)
			assert.Equal(t, []Key{{Variant: variant, Scenario: "a", Item: "0", Repeat: 2}}, d.Rerun, "shared baseline is scheduled only once")
		})
	}
}
func TestK19cContractRejectsForgedAndChangedPrefixes(t *testing.T) {
	e := decisionEvidence(t)
	e.Variants[0].Success.Status = Inconclusive
	first, err := Decide(e, nil)
	require.NoError(t, err)
	next := e
	next.Look = 2
	next.Variants = slices.Clone(e.Variants[:1])
	for _, tc := range []struct {
		name   string
		change func(*Decision, *Evaluation)
	}{
		{"forged scheduling", func(d *Decision, _ *Evaluation) { d.Status = Incomplete }},
		{"duplicate variant", func(d *Decision, _ *Evaluation) { d.Variants = append(d.Variants, d.Variants[0]) }},
		{"variant added", func(_ *Decision, e *Evaluation) {
			v := e.Variants[0]
			v.Variant = "third"
			e.Policy.Unsupported["third"] = .02
			e.Variants = append(e.Variants, v)
		}},
		{"policy drift", func(_ *Decision, e *Evaluation) { e.Policy.Unsupported["receipt_only"] = .05 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := first
			d.Variants = slices.Clone(first.Variants)
			n := next
			n.Policy = clonePolicy(next.Policy)
			n.Variants = slices.Clone(next.Variants)
			tc.change(&d, &n)
			_, err := Decide(n, []Decision{d})
			require.Error(t, err)
		})
	}
}
func TestK19cContractFinalPrecisionAndSameLookPin(t *testing.T) {
	e := decisionEvidence(t)
	e.Look = 3
	e.Variants[0].Recall.Status = Inconclusive
	d, err := DecideMode(e, nil, Offline)
	require.NoError(t, err)
	assert.Equal(t, Complete, d.Status)
	assert.Equal(t, NotPassed, d.Variants[0].Status)
	assert.Equal(t, Pass, d.Variants[1].Status)
	e = decisionEvidence(t)
	e.Variants[0].Success.Status = Inconclusive
	e.Variants[1].Missing = []Key{{Variant: "hybrid", Scenario: "a", Item: "0", Repeat: 1}}
	d, err = Decide(e, nil)
	require.NoError(t, err)
	e.Variants[1].Missing = nil
	next, err := Decide(e, []Decision{d})
	require.NoError(t, err)
	assert.Equal(t, d.Variants[0], next.Variants[0])
	e.Variants[0].Success.Status = Pass
	_, err = Decide(e, []Decision{d})
	require.Error(t, err, "same-look decided observation cannot be improved by sibling rerun")
}

func TestK19cContractMalformedGlobalBarrier(t *testing.T) {
	e := decisionEvidence(t)
	e.Variants[0].Missing = []Key{{Variant: "receipt_only", Scenario: "a", Item: "0", Repeat: 1}}
	first, err := Decide(e, nil)
	require.NoError(t, err)
	next := e
	next.Look = 2
	forged := first
	forged.Status = Continue
	_, err = Decide(next, []Decision{forged})
	require.Error(t, err, "global CONTINUE cannot hide an incomplete sibling")
	forged = first
	forged.Variants = slices.Clone(first.Variants[:1])
	next.Look = 1
	_, err = Decide(next, []Decision{forged})
	require.Error(t, err, "terminal sibling cannot disappear")
}

func TestK19cContractRejectsStaleActiveLook(t *testing.T) {
	e := decisionEvidence(t)
	e.Variants[0].Success.Status = Inconclusive
	first, err := Decide(e, nil)
	require.NoError(t, err)
	second := first
	second.Look = 2
	next := e
	next.Look = 3
	next.Variants = slices.Clone(e.Variants[:1])
	_, err = Decide(next, []Decision{first, second})
	require.Error(t, err, "active variant cannot reuse look1 evidence as a look2 decision")
}

func TestK19cContractInitialPolicyCoverage(t *testing.T) {
	for _, single := range []bool{false, true} {
		cfg := policyConfig()
		cfg.Variants = cfg.Variants[:1]
		if single {
			delete(cfg.Policy.Unsupported, "hybrid")
		}
		e, err := Evaluate(testTable(48), cfg, 1)
		require.NoError(t, err)
		d, err := Decide(e, nil)
		if !single {
			require.Error(t, err, "initial operational decision must cover all eligible policy variants")
		} else {
			require.NoError(t, err)
			require.Len(t, d.Variants, 1)
			assert.Equal(t, Complete, d.Status)
		}
	}
}
