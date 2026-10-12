package eval

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportRejectsMissingInputs(t *testing.T) {
	e, a := &Experiment{}, &StoredAttempt{}
	for _, tc := range []struct {
		name   string
		render func(io.Writer) error
	}{
		{"show experiment", func(w io.Writer) error {
			return Show(w, nil, a, ShowOptions{})
		}},
		{"show attempt", func(w io.Writer) error {
			return Show(w, e, nil, ShowOptions{})
		}},
		{"show json experiment", func(w io.Writer) error {
			return ShowJSON(w, nil, a, ShowOptions{})
		}},
		{"show json attempt", func(w io.Writer) error {
			return ShowJSON(w, e, nil, ShowOptions{})
		}},
		{"summary", func(w io.Writer) error {
			return Summary(w, nil)
		}},
		{"summary json", func(w io.Writer) error {
			return SummaryJSON(w, nil)
		}},
		{"comparison", func(w io.Writer) error {
			return WriteComparisons(w, []*Comparison{{}, nil})
		}},
		{"comparison json", func(w io.Writer) error {
			return WriteComparisonsJSON(w, []*Comparison{{}, nil})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.Error(t, tc.render(&out))
			assert.Empty(t, out.String())
		})
	}
}

func TestCompareRejectsInvalidInputs(t *testing.T) {
	base := comparisonExperiment("baseline", "base", "one")
	cand := comparisonExperiment("candidate", "other", "one")
	multiple := comparisonExperiment("multiple", "base", "one")
	addReportVariant(multiple, "extra", VerdictPassed)
	for _, tc := range []struct {
		name    string
		compare func() error
	}{
		{"nil baseline", func() error {
			_, err := Compare(Side{}, Side{Experiment: cand}, CompareOptions{})
			return err
		}},
		{"nil candidate", func() error {
			_, err := Compare(Side{Experiment: base}, Side{}, CompareOptions{})
			return err
		}},
		{"nil experiments", func() error {
			_, err := CompareExperiments(nil, cand, CompareOptions{})
			return err
		}},
		{"nil variants experiment", func() error {
			_, err := CompareVariants(nil, []string{"a", "b"})
			return err
		}},
		{"unselected variant", func() error {
			_, err := Compare(Side{Experiment: multiple}, Side{Experiment: cand}, CompareOptions{})
			return err
		}},
		{"unknown variant", func() error {
			_, err := Compare(Side{Experiment: base, Variant: "missing"}, Side{Experiment: cand}, CompareOptions{})
			return err
		}},
		{"empty variant", func() error {
			_, err := CompareVariants(multiple, []string{"base", ""})
			return err
		}},
		{"disjoint variants", func() error {
			_, err := CompareExperiments(multiple, cand, CompareOptions{})
			return err
		}},
		{"wrong parent", func() error {
			_, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{Parent: multiple})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.compare())
		})
	}
}
