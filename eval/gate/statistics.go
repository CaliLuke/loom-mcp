package gate

import (
	"math"
	"slices"
)

// PercentileIndices returns zero-based equal-tail percentile indices.
func PercentileIndices(n int, alpha float64) (int, int) {
	return int(math.Floor(float64(n) * alpha / 2)), int(math.Ceil(float64(n)*(1-alpha/2))) - 1
}

// Criterion uses strict bounds, including the recall margin (Q17).
func Criterion(i Interval, margin float64, higher bool) CriterionResult {
	out := CriterionResult{Interval: i, Status: Inconclusive}
	if higher {
		if i.Lo > margin {
			out.Status = Pass
		} else if i.Hi < margin {
			out.Status = Fail
		}
	} else {
		if i.Hi < margin {
			out.Status = Pass
		} else if i.Lo > margin {
			out.Status = Fail
		}
	}
	return out
}

// Recall pools qualifying outcomes and adjusts Wilson's effective sample size
// for agreement between the two qualifying repeats of each item.
func Recall(rows Table, variant string, scenarios []string, alpha float64) RecallResult {
	groups := map[[2]string][]bool{}
	n, success := 0, 0
	for _, r := range rows {
		if r.Variant != variant || r.Infrastructure || !r.Qualifying || !slices.Contains(scenarios, r.Scenario) {
			continue
		}
		groups[[2]string{r.Scenario, r.Item}] = append(groups[[2]string{r.Scenario, r.Item}], r.Recalled)
		n++
		if r.Recalled {
			success++
		}
	}
	out := RecallResult{N: n, Deff: 1, CriterionResult: CriterionResult{Status: Inconclusive}}
	if n == 0 {
		out.Reason = "no qualifying runs"
		return out
	}
	p := float64(success) / float64(n)
	agree, both := 0, 0
	for _, g := range groups {
		if len(g) == 2 {
			both++
			if g[0] == g[1] {
				agree++
			}
		}
	}
	expected := p*p + (1-p)*(1-p)
	if expected == 1 {
		out.Rho = 1
	} else if both > 0 {
		out.Rho = max(0, min(1, (float64(agree)/float64(both)-expected)/(1-expected)))
	}
	m := float64(n) / float64(len(groups))
	out.Deff = max(1, 1+(m-1)*out.Rho)
	out.EffectiveN = float64(n) / out.Deff
	out.CriterionResult = Criterion(wilson(p, out.EffectiveN, alpha), .8, true)
	return out
}
func wilson(p, n, alpha float64) Interval {
	z := math.Sqrt2 * math.Erfinv(1-alpha)
	z2 := z * z
	denom := 1 + z2/n
	center := (p + z2/(2*n)) / denom
	half := z * math.Sqrt(p*(1-p)/n+z2/(4*n*n)) / denom
	return Interval{Estimate: p, Lo: max(0, center-half), Hi: min(1, center+half)}
}
func binary(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
