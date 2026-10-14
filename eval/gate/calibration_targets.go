package gate

import "fmt"

type targetRange struct {
	count     int
	low, high float64
}

// LegacyCalibrationTargets renders historical v1 measured targets from saved simulation outcomes.
// Guard attribution is a lower bound: old cells retain criterion marginals at
// stopping, not joint guard causes. Their union bound safely bounds non-guard
// failures, including failures at later looks. No simulations are rerun here.
func LegacyCalibrationTargets(cells []CellReport) []string {
	ranges := map[string]*targetRange{}
	look1, guard := targetRange{}, targetRange{}
	best := 0.0
	for _, c := range cells {
		if c.Infeasible != "" {
			continue
		}
		best = max(best, c.Pass)
		axis := c.Cell.axis()
		if axis == "" {
			continue
		}
		if ranges[axis] == nil {
			ranges[axis] = &targetRange{}
		}
		ranges[axis].add(c.Pass)
		if axis == measureNull {
			look1.add(c.LookFail[0])
			nonGuard := 0.0
			for _, dist := range c.Final {
				nonGuard += dist[Fail]
			}
			guard.add(max(0, c.LookFail[0]-nonGuard))
		}
	}
	var out []string
	for _, axis := range []string{measureNull, measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
		out = append(out, calibrationTargetLine(axis, ranges[axis]))
	}
	out = append(out, fmt.Sprintf("best whole-gate PASS over all feasible cells: %.4f", best))
	if look1.count > 0 {
		out = append(out, fmt.Sprintf("null look-1 FAIL range %.4f..%.4f; guard look-1 FAIL lower-bound range %.4f..%.4f (criterion-failure union bound across all stopping looks)", look1.low, look1.high, guard.low, guard.high))
		if guard.low > .2 {
			out = append(out, "limiting factor: guard/look-1 FAIL; the guard alone prevents the null PASS >= 0.8000 target even when variants equal baseline")
		}
	}
	return out
}

func (r *targetRange) add(v float64) {
	if r.count == 0 {
		r.low, r.high = v, v
	} else {
		r.low = min(r.low, v)
		r.high = max(r.high, v)
	}
	r.count++
}

// calibrationTargetLine reports missing cells explicitly rather than implying a pass.
func calibrationTargetLine(axis string, r *targetRange) string {
	label, op, target := axis+" margin PASS", "<=", .05
	if axis == measureNull {
		label, op, target = "null PASS", ">=", .8
	}
	if r == nil {
		return fmt.Sprintf("%s %s %.4f: not measured", label, op, target)
	}
	value, met := r.high, r.high <= target
	if axis == measureNull {
		value, met = r.low, r.low >= target
	}
	status := "met"
	if !met {
		status = "NOT MET; the gate cannot certify this target at 48 items per scenario"
	}
	return fmt.Sprintf("%s %s %.4f: measured %.4f (%s)", label, op, target, value, status)
}
