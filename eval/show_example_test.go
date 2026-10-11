package eval_test

import (
	"bytes"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/require"
)

func TestShowCompareExample(t *testing.T) {
	var out bytes.Buffer
	baseline := &eval.Experiment{Header: eval.ExperimentHeader{ID: "baseline", Variants: []eval.StoredVariant{{ID: "base"}}}}
	candidate := &eval.Experiment{Header: eval.ExperimentHeader{ID: "candidate", Variants: []eval.StoredVariant{{ID: "changed"}}}}
	attempt := &eval.StoredAttempt{AttemptKey: eval.AttemptKey{Variant: "changed", Scenario: "chat", Item: "one", Repeat: 1}, Verdict: eval.VerdictPassed}
	baseline.Attempts = []*eval.StoredAttempt{{AttemptKey: eval.AttemptKey{Variant: "base", Scenario: "chat", Item: "one", Repeat: 1}, Verdict: eval.VerdictPassed}}
	candidate.Attempts = []*eval.StoredAttempt{attempt}
	render := func() error {
		// snippet:start eval-show-compare
		if err := eval.Show(&out, candidate, attempt, eval.ShowOptions{
			Render: trajectory.RenderOptions{Limits: trajectory.DefaultLimits()},
		}); err != nil {
			return err
		}
		comparisons, err := eval.CompareExperiments(baseline, candidate, eval.CompareOptions{})
		if err != nil {
			return err
		}
		return eval.WriteComparisonsJSON(&out, comparisons)
		// snippet:end eval-show-compare
	}
	require.NoError(t, render())
}
