package gate

import (
	"fmt"
	"slices"
	"strings"
)

// Render reports scheduling separately from qualification, using each variant's
// own immutable stopping snapshot. Missing keys are an exact rerun manifest.
func Render(_ Evaluation, d Decision) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Look %d scheduling: %s (COMPLETE is not qualification)\n", d.Look, d.Status)
	for _, decision := range d.Variants {
		v := decision.Evidence
		fmt.Fprintf(&out, "%s: %s at look %d\n", decision.Variant, decision.Status, decision.Look)
		if decision.Reason != "" {
			out.WriteString(decision.Reason + "\n")
		}
		for _, c := range criteria(v) {
			fmt.Fprintf(&out, "%s/%s: %s estimate=%.6f interval=[%.6f, %.6f]\n", v.Variant, c.name, c.result.Status, c.result.Estimate, c.result.Lo, c.result.Hi)
			if c.result.Reason != "" {
				out.WriteString(c.result.Reason + "\n")
			}
		}
		fmt.Fprintf(&out, "%s/recall: n=%d effective_n=%.6f rho=%.6f deff=%.6f\n", v.Variant, v.Recall.N, v.Recall.EffectiveN, v.Recall.Rho, v.Recall.Deff)
		names := make([]string, 0, len(v.ScenarioIntervals))
		for name := range v.ScenarioIntervals {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			i := v.ScenarioIntervals[name]
			fmt.Fprintf(&out, "%s/%s: success difference %.6f interval=[%.6f, %.6f] (guard veto only; crossing does not certify scenario)\n", v.Variant, name, i.Estimate, i.Lo, i.Hi)
		}
	}
	for _, key := range d.Rerun {
		fmt.Fprintf(&out, "rerun: %s/%s/%s repeat=%d\n", key.Variant, key.Scenario, key.Item, key.Repeat)
	}
	return out.String()
}
