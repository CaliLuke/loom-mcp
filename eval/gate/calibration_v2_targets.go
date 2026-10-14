package gate

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type variantTargets struct {
	pass                          map[string]*targetRange
	se                            map[string]*targetRange
	look1, guard, criterion, cost targetRange
	masks                         map[string]*targetRange
	infeasible                    int
}

// CalibrationTargets renders explicit v2 candidate and per-variant evidence.
// Combined PASS is informational and never enters these eligibility comparisons.
func CalibrationTargets(cells []CellReport, policy *Policy) ([]string, error) {
	if policy == nil {
		return nil, fmt.Errorf("gate: target report requires policy")
	}
	names := slices.Sorted(maps.Keys(policy.Unsupported))
	if err := policy.Validate(names); err != nil {
		return nil, err
	}
	var lines []string
	for _, name := range names {
		target, err := collectVariantTargets(cells, name, policy.Unsupported[name])
		if err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("%s candidate unsupported margin %.2f; infeasible cells %d", name, policy.Unsupported[name], target.infeasible))
		for _, axis := range []string{measureNull, measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
			line := name + " " + calibrationTargetLine(axis, target.pass[axis])
			if se := target.se[axis]; se != nil {
				line += fmt.Sprintf("; cells %d/9; MC SE %.4f..%.4f", se.count, se.low, se.high)
			}
			lines = append(lines, line)
		}
		if target.look1.count > 0 {
			lines = append(lines, fmt.Sprintf("%s null look-1 FAIL %.4f..%.4f; measured guard stop %.4f..%.4f; criterion stop %.4f..%.4f", name, target.look1.low, target.look1.high, target.guard.low, target.guard.high, target.criterion.low, target.criterion.high))
			causes := []string{}
			for _, mask := range slices.Sorted(maps.Keys(target.masks)) {
				r := target.masks[mask]
				causes = append(causes, fmt.Sprintf("%s=%.4f..%.4f", mask, r.low, r.high))
			}
			lines = append(lines, name+" null joint stopping masks: "+strings.Join(causes, ", "))
		}
		if target.cost.count > 0 {
			lines = append(lines, fmt.Sprintf("%s scheduled baseline/active-variant attempt cost %.4f..%.4f (shared schedule, repeats included)", name, target.cost.low, target.cost.high))
		}
	}
	lines = append(lines, "Measured Monte Carlo comparisons are eligibility diagnostics, not a proof of population error rates.")
	return lines, nil
}

func collectVariantTargets(cells []CellReport, name string, margin float64) (variantTargets, error) {
	out := variantTargets{pass: map[string]*targetRange{}, se: map[string]*targetRange{}, masks: map[string]*targetRange{}}
	nulls := []VariantSimulation{}
	for _, c := range cells {
		if c.Infeasible != "" {
			out.infeasible++
			continue
		}
		out.cost.add(c.ExpectedAttempts)
		axis := c.Cell.axisForMargin(margin)
		if axis == "" {
			continue
		}
		v, ok := c.Variants[name]
		if !ok {
			return out, fmt.Errorf("gate: missing per-variant target observations for %s", name)
		}
		if out.pass[axis] == nil {
			out.pass[axis] = &targetRange{}
			out.se[axis] = &targetRange{}
		}
		out.pass[axis].add(v.Pass)
		out.se[axis].add(v.SE)
		if axis == measureNull {
			out.look1.add(v.LookFail[0])
			out.guard.add(v.GuardStop)
			out.criterion.add(v.CriterionStop)
			nulls = append(nulls, v)
			for mask := range v.FailureMasks {
				out.masks[mask] = &targetRange{}
			}
		}
	}
	// Include zero-frequency cells in every observed joint-mask range.
	for _, v := range nulls {
		for mask, r := range out.masks {
			r.add(v.FailureMasks[mask])
		}
	}
	return out, nil
}
