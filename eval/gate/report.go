package gate

import (
	"fmt"
	"slices"
	"strings"
)

// Render produces a deterministic text report without conflating inconclusive
// criteria with passing criteria. Missing keys are an exact rerun manifest.
func Render(e Evaluation, d Decision) string {
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Look %d: %s\n", d.Look, d.Status))
	if d.Reason != "" {
		out.WriteString(d.Reason + "\n")
	}
	for _, v := range e.Variants {
		for _, c := range criteria(v) {
			out.WriteString(fmt.Sprintf("%s/%s: %s estimate=%.6f interval=[%.6f, %.6f]\n", v.Variant, c.name, c.result.Status, c.result.Estimate, c.result.Lo, c.result.Hi))
			if c.result.Reason != "" {
				out.WriteString(c.result.Reason + "\n")
			}
		}
		out.WriteString(fmt.Sprintf("%s/recall: n=%d effective_n=%.6f rho=%.6f deff=%.6f\n", v.Variant, v.Recall.N, v.Recall.EffectiveN, v.Recall.Rho, v.Recall.Deff))
		names := make([]string, 0, len(v.Scenarios))
		for s := range v.Scenarios {
			names = append(names, s)
		}
		slices.Sort(names)
		for _, s := range names {
			out.WriteString(fmt.Sprintf("%s/%s: success difference %.6f\n", v.Variant, s, v.Scenarios[s]))
		}
	}
	for _, k := range d.Rerun {
		out.WriteString(fmt.Sprintf("rerun: %s/%s/%s repeat=%d\n", k.Variant, k.Scenario, k.Item, k.Repeat))
	}
	return out.String()
}
