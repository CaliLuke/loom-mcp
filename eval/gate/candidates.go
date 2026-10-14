package gate

import (
	"fmt"
	"math"
	"slices"
)

// SelectCandidates selects the smallest fully measured eligible candidate per
// variant. Missing or duplicate decision cells are malformed, not ineligibility.
func SelectCandidates(candidates []CandidateReport, variants []string) ([]CandidateSelection, error) {
	if len(candidates) != 3 {
		return nil, fmt.Errorf("gate: all three candidates are required")
	}
	candidates = slices.Clone(candidates)
	slices.SortFunc(candidates, func(a, b CandidateReport) int {
		if a.Margin < b.Margin {
			return -1
		}
		if a.Margin > b.Margin {
			return 1
		}
		return 0
	})
	selections := make([]CandidateSelection, len(variants))
	for i, v := range variants {
		selections[i] = CandidateSelection{Variant: v, Status: CalibrationIneligible}
	}
	for i, candidate := range candidates {
		if candidate.Margin != []float64{.02, .03, .05}[i] {
			return nil, fmt.Errorf("gate: duplicate or invalid candidate margin")
		}
		for j, variant := range variants {
			result, eligible, err := assessCandidate(candidate, variant)
			if err != nil {
				return nil, err
			}
			if eligible && selections[j].Margin == 0 {
				selections[j] = result
			}
		}
	}
	return selections, nil
}
func assessCandidate(candidate CandidateReport, variant string) (CandidateSelection, bool, error) {
	result := CandidateSelection{Variant: variant, Margin: candidate.Margin, Status: CalibrationIneligible, WorstNull: 1, WorstBoundary: map[string]float64{}}
	rows := map[Cell]CellReport{}
	for _, r := range candidate.Report.Cells {
		if _, ok := rows[r.Cell]; ok {
			return result, false, fmt.Errorf("gate: duplicate candidate cell")
		}
		rows[r.Cell] = r
	}
	eligible := true
	for _, c := range CalibrationGrid() {
		axis := c.axisForMargin(candidate.Margin)
		if axis == "" {
			continue
		}
		row, ok := rows[c]
		if !ok {
			return result, false, fmt.Errorf("gate: missing candidate decision cell %+v", c)
		}
		if row.Infeasible != "" {
			eligible = false
			continue
		}
		outcome, ok := row.Variants[variant]
		if !ok || row.Simulations != 2000 || math.IsNaN(outcome.Pass) || outcome.Pass < 0 || outcome.Pass > 1 {
			return result, false, fmt.Errorf("gate: invalid candidate evidence for %s", variant)
		}
		if axis == measureNull {
			result.WorstNull = min(result.WorstNull, outcome.Pass)
		} else {
			result.WorstBoundary[axis] = max(result.WorstBoundary[axis], outcome.Pass)
		}
	}
	eligible = eligible && candidateMeetsTargets(result)
	if eligible {
		result.Status = CalibrationEligible
	}
	return result, eligible, nil
}

// CandidateDecisionCell identifies the null or a boundary for this candidate.
func (c Cell) CandidateDecisionCell(margin float64) bool {
	return c.axisForMargin(margin) != ""
}
func (c Cell) axisForMargin(margin float64) string {
	if c.DU != 0 {
		if c.DY == 0 && c.DU == margin && c.DE == 0 && c.Recall == .9 {
			return measureUnsupported
		}
		return ""
	}
	return c.axis()
}

// candidateMeetsTargets applies the null and boundary limits to measured maxima.
func candidateMeetsTargets(result CandidateSelection) bool {
	eligible := result.WorstNull >= .8
	for _, axis := range []string{measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
		p, ok := result.WorstBoundary[axis]
		eligible = eligible && ok && p <= .05
	}
	return eligible
}
