package suite_test

import (
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lookEvidence() gate.Evaluation {
	pass := gate.CriterionResult{Status: gate.Pass}
	return gate.Evaluation{Policy: suite.ProposedOfflinePolicy(), Baseline: "baseline", Look: 1, Variants: []gate.VariantResult{
		{Variant: "receipt_only", Success: pass, Unsupported: pass, Evidence: pass, Recall: gate.RecallResult{CriterionResult: pass}},
		{Variant: "hybrid", Success: pass, Unsupported: pass, Evidence: pass, Recall: gate.RecallResult{CriterionResult: pass}},
	}}
}

func TestK19cContractLookPlanTransitions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receipt gate.Status
		hybrid  gate.Status
		look    int
		want    []string
		rerun   bool
		reject  bool
	}{
		{"failed sibling", gate.Fail, gate.Inconclusive, 2, []string{"baseline", "hybrid"}, false, false},
		{"passed sibling", gate.Pass, gate.Inconclusive, 2, []string{"baseline", "hybrid"}, false, false},
		{"continue incomplete", gate.Inconclusive, gate.Incomplete, 1, []string{"baseline", "receipt_only", "hybrid", "clear_only"}, true, false},
		{"pass incomplete", gate.Pass, gate.Incomplete, 1, []string{"baseline", "receipt_only", "hybrid", "clear_only"}, true, false},
		{"global barrier", gate.Inconclusive, gate.Incomplete, 2, nil, false, true},
		{"both terminal", gate.Pass, gate.Fail, 2, nil, false, true},
		{"repeat decided", gate.Inconclusive, gate.Inconclusive, 1, nil, false, true},
		{"skip partition", gate.Inconclusive, gate.Inconclusive, 3, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := lookEvidence()
			e.Variants[0].Success.Status = tc.receipt
			e.Variants[1].Success.Status = tc.hybrid
			if tc.hybrid == gate.Incomplete {
				e.Variants[1].Success.Status = gate.Pass
				e.Variants[1].Missing = []gate.Key{{Variant: "hybrid", Scenario: "a", Item: "i", Repeat: 2}}
			}
			d, err := gate.Decide(e, nil)
			require.NoError(t, err)
			work, err := suite.PlanLook(e.Policy, tc.look, []gate.Decision{d})
			if tc.reject {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, work.Variants)
			assert.Equal(t, tc.rerun, work.Rerun)
			if tc.rerun {
				assert.Equal(t, []eval.AttemptKey{{Variant: "hybrid", Scenario: "a", Item: "i", Repeat: 2}}, work.Keys)
			} else {
				assert.Nil(t, work.Keys)
			}
		})
	}
}

func TestK19cContractLookPlanEligibleAndBaselineIdentity(t *testing.T) {
	p := suite.ProposedOfflinePolicy()
	delete(p.Unsupported, "receipt_only")
	work, err := suite.PlanLook(p, 1, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"baseline", "hybrid", "clear_only"}, work.Variants)
	_, err = suite.PlanLook(p, 2, nil)
	require.Error(t, err)
	e := lookEvidence()
	e.Variants[0].Missing = []gate.Key{{Variant: "baseline", Scenario: "a", Item: "i", Repeat: 2}}
	e.Variants[1].Missing = e.Variants[0].Missing
	d, err := gate.Decide(e, nil)
	require.NoError(t, err)
	work, err = suite.PlanLook(e.Policy, 1, []gate.Decision{d})
	require.NoError(t, err)
	assert.Equal(t, []eval.AttemptKey{{Variant: "baseline", Scenario: "a", Item: "i", Repeat: 2}}, work.Keys)
}

func TestK19cContractLookPlanRejectsForgedRerun(t *testing.T) {
	e := lookEvidence()
	e.Variants[1].Missing = []gate.Key{{Variant: "hybrid", Scenario: "a", Item: "i", Repeat: 2}}
	d, err := gate.Decide(e, nil)
	require.NoError(t, err)
	d.Rerun = []gate.Key{{Variant: "receipt_only", Scenario: "a", Item: "i", Repeat: 1}}
	_, err = suite.PlanLook(e.Policy, 1, []gate.Decision{d})
	require.Error(t, err, "global rerun manifest must match retained incomplete observations")
}
