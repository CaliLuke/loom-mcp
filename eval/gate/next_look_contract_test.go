package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextLookRejectsChangedPolicyAndCorruptPrefixBeforeScheduling(t *testing.T) {
	// This is a state-machine contract test: no resampling or calibration runs.
	policy := proposedTestPolicy()
	evaluation := Evaluation{Look: 1, Policy: policy, Variants: []VariantResult{
		{Variant: "receipt_only", Success: CriterionResult{Status: Inconclusive}, Unsupported: CriterionResult{Status: Pass}, Evidence: CriterionResult{Status: Pass}, Recall: RecallResult{CriterionResult: CriterionResult{Status: Pass}}},
		{Variant: "hybrid", Success: CriterionResult{Status: Pass}, Unsupported: CriterionResult{Status: Pass}, Evidence: CriterionResult{Status: Pass}, Recall: RecallResult{CriterionResult: CriterionResult{Status: Pass}}},
	}}
	first, err := Decide(evaluation, nil)
	require.NoError(t, err)
	assert.Equal(t, Continue, first.Status)
	require.NoError(t, ValidateNextLook(policy, 1, nil))
	require.NoError(t, ValidateNextLook(policy, 2, []Decision{first}))
	for _, tt := range []struct {
		name string
		edit func(*Policy, *Decision)
		look int
		want string
	}{
		{"invalid index", func(*Policy, *Decision) {
		}, 0, "invalid next-look"},
		{"invalid policy", func(p *Policy, _ *Decision) {
			p.Alpha = -1
		}, 2, "unsupported decision policy"},
		{"changed policy", func(p *Policy, _ *Decision) {
			p.Unsupported["receipt_only"] = .05
		}, 2, "policy changed"},
		{"out of order", func(*Policy, *Decision) {
		}, 3, "out-of-order"},
		{"changed outcome", func(_ *Policy, d *Decision) {
			d.Variants[0].Status = Pass
		}, 2, "contradicts retained evidence"},
		{"forged rerun", func(_ *Policy, d *Decision) {
			d.Rerun = []Key{{Scenario: "a", Item: "one", Variant: "receipt_only", Repeat: 1}}
		}, 2, "rerun manifest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nextPolicy := clonePolicy(policy)
			prefix := Decision{Look: first.Look, Status: first.Status, Policy: clonePolicy(first.Policy), Variants: []VariantDecision{cloneVariantDecision(first.Variants[0]), cloneVariantDecision(first.Variants[1])}}
			tt.edit(nextPolicy, &prefix)
			require.ErrorContains(t, ValidateNextLook(nextPolicy, tt.look, []Decision{prefix}), tt.want)
		})
	}
	require.Error(t, ValidateNextLook(nil, 1, nil))
}
