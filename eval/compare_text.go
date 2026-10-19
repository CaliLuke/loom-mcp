package eval

import (
	"fmt"
	"io"
	"strings"
)

// WriteComparisons renders regressions first, collapsing unchanged groups.
// Comparisons of multiple variants in one experiment share a column table.
func WriteComparisons(w io.Writer, cs []*Comparison) error {
	for _, c := range cs {
		if c == nil {
			return fmt.Errorf("eval: nil comparison")
		}
	}
	var out strings.Builder
	if variantColumns(cs) {
		writeVariantColumns(&out, cs)
	} else {
		for i, c := range cs {
			if i > 0 {
				out.WriteByte('\n')
			}
			fmt.Fprintf(&out, "Compare %s [%s] -> %s [%s]\n", c.Baseline.ID, c.Baseline.Variant, c.Candidate.ID, c.Candidate.Variant)
			writeComparisonContext(&out, c)
			writeGroupSections(&out, c)
			out.WriteString("Totals:\n")
			writeSideTotals(&out, "baseline", c.Totals.Baseline)
			writeSideTotals(&out, "candidate", c.Totals.Candidate)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

// WriteComparisonsJSON writes deterministic version-1 comparison documents.
func WriteComparisonsJSON(w io.Writer, cs []*Comparison) error {
	for _, c := range cs {
		if c == nil {
			return fmt.Errorf("eval: nil comparison")
		}
	}
	version := 1
	for _, c := range cs {
		if c.Baseline.Version == 2 || c.Candidate.Version == 2 {
			version = 2
		}
	}
	return writeReportJSON(w, struct {
		Format      string        `json:"format"`
		Version     int           `json:"version"`
		Comparisons []*Comparison `json:"comparisons"`
	}{Format: "loom-mcp.eval.compare", Version: version, Comparisons: cs})
}

func writeComparisonContext(out *strings.Builder, c *Comparison) {
	for _, warning := range c.Warnings {
		fmt.Fprintf(out, "Warning [%s]: %s\n", warning.Code, warning.Message)
	}
	for _, side := range []struct {
		name  string
		pairs []Pair
	}{{"Only baseline", c.OnlyBaseline}, {"Only candidate", c.OnlyCandidate}} {
		for _, pair := range side.pairs {
			fmt.Fprintf(out, "%s: %s/%s\n", side.name, pair.Scenario, pair.Item)
		}
	}
}

func writeGroupSections(out *strings.Builder, c *Comparison) {
	for _, section := range comparisonSections() {
		fmt.Fprintf(out, "%s:\n", section.title)
		for _, g := range c.Groups {
			if g.Status != section.status {
				continue
			}
			if g.Baseline.Probes+g.Candidate.Probes > 0 {
				fmt.Fprintf(out, "  %s/%s quality not_assessed\n", g.Scenario, g.Item)
				writeGroupDetails(out, g)
				continue
			}
			fmt.Fprintf(out, "  %s/%s %d/%d -> %d/%d passed%s\n", g.Scenario, g.Item, g.Baseline.Passed, g.Baseline.Ran, g.Candidate.Passed, g.Candidate.Ran, judgmentFlags(g.Uncalibrated, g.TruncatedJudgments, g.Judgments))
			writeGroupDetails(out, g)
		}
	}
	unchanged := 0
	for _, g := range c.Groups {
		if g.Status == statusUnchanged {
			unchanged++
		}
	}
	fmt.Fprintf(out, "Unchanged: %d items\n", unchanged)
}

func writeGroupDetails(out *strings.Builder, g GroupDelta) {
	for _, d := range g.Checks {
		fmt.Fprintf(out, "    Check %s %d/%d -> %d/%d: %s\n", d.Name, d.Baseline.Passed, d.Baseline.Total, d.Candidate.Passed, d.Candidate.Total, d.Diagnostic)
	}
	for _, d := range g.Claims {
		fmt.Fprintf(out, "    Claim %s (%s", d.ClaimID, d.Subject)
		if d.Tool != "" {
			fmt.Fprintf(out, " %s", d.Tool)
		}
		fmt.Fprintf(out, ") %d/%d -> %d/%d entailed; errors %d -> %d%s: %s\n", d.Baseline.Entailed, d.Baseline.Total, d.Candidate.Entailed, d.Candidate.Total, d.Baseline.Errors, d.Candidate.Errors, judgmentFlags(d.Uncalibrated, d.Baseline.Truncated+d.Candidate.Truncated, d.Baseline.Total+d.Candidate.Total), d.Rationale)
	}
	for _, d := range g.Scores {
		fmt.Fprintf(out, "    Score %s %s -> %s", d.Name, scoreText(d.Baseline), scoreText(d.Candidate))
		if d.Threshold != nil {
			fmt.Fprintf(out, " threshold %g", *d.Threshold)
		}
		out.WriteByte('\n')
	}
	fmt.Fprintf(out, "    Median tokens %d -> %d; duration %s -> %s\n", g.Baseline.MedianTokens, g.Candidate.MedianTokens, g.Baseline.MedianDuration, g.Candidate.MedianDuration)
	if g.Baseline.ToolsUnavailable+g.Candidate.ToolsUnavailable > 0 {
		fmt.Fprintf(out, "    tools_unavailable %d -> %d\n", g.Baseline.ToolsUnavailable, g.Candidate.ToolsUnavailable)
	}
	if d := g.Tools; d != nil {
		fmt.Fprintf(out, "    Tools [%s] (%d/%d) -> [%s] (%d/%d)\n", strings.Join(d.Baseline.Sequence, ", "), d.Baseline.Count, d.Baseline.Total, strings.Join(d.Candidate.Sequence, ", "), d.Candidate.Count, d.Candidate.Total)
		for _, edit := range d.Edits {
			fmt.Fprintf(out, "      %s %s\n", edit.Op, edit.Name)
		}
	}
}

func writeSideTotals(out *strings.Builder, name string, s GroupStats) {
	if s.Probes > 0 || s.Quality == probeQualityNotAssessed {
		fmt.Fprintf(out, "  %s quality not_assessed; %d probes; median tokens %d; duration %s; tools_unavailable %d\n", name, s.Probes, s.MedianTokens, s.MedianDuration, s.ToolsUnavailable)
		return
	}
	fmt.Fprintf(out, "  %s %d/%d passed; median tokens %d; duration %s; tools_unavailable %d\n", name, s.Passed, s.Ran, s.MedianTokens, s.MedianDuration, s.ToolsUnavailable)
}

func judgmentFlags(uncalibrated bool, truncated, total int) string {
	flags := ""
	if uncalibrated {
		flags += " [uncalibrated]"
	}
	if truncated > 0 {
		flags += fmt.Sprintf(" [truncated %d/%d]", truncated, total)
	}
	return flags
}

func scoreText(value *float64) string {
	if value == nil {
		return "absent"
	}
	return fmt.Sprintf("%g", *value)
}

func comparisonSections() []struct{ status, title string } {
	return []struct{ status, title string }{{statusRegressed, "Regressed"}, {statusImproved, "Improved"}, {statusNotComparable, "Not comparable"}}
}

func variantColumns(cs []*Comparison) bool {
	if len(cs) < 2 {
		return false
	}
	first := cs[0].Baseline
	for _, c := range cs {
		if c.Baseline != first || c.Candidate.ID != first.ID {
			return false
		}
	}
	return true
}

// writeVariantColumns keeps one baseline column and a column per candidate.
func writeVariantColumns(out *strings.Builder, cs []*Comparison) {
	fmt.Fprintf(out, "Compare variants of %s\n", cs[0].Baseline.ID)
	for _, c := range cs {
		writeComparisonContext(out, c)
	}
	var pairs []Pair
	for _, c := range cs {
		for _, g := range c.Groups {
			pairs = orderedNames(pairs, []Pair{{g.Scenario, g.Item}})
		}
	}
	for _, section := range comparisonSections() {
		fmt.Fprintf(out, "%s:\n", section.title)
		writeVariantHeading(out, cs)
		for _, pair := range pairs {
			if variantPairStatus(cs, pair) != section.status {
				continue
			}
			writeVariantRow(out, cs, pair)
		}
	}
	unchanged := 0
	for _, pair := range pairs {
		if variantPairStatus(cs, pair) == statusUnchanged {
			unchanged++
		}
	}
	fmt.Fprintf(out, "Unchanged: %d items\nTotals:\n", unchanged)
	for _, c := range cs {
		writeSideTotals(out, fmt.Sprintf("%s (vs %s)", c.Baseline.Variant, c.Candidate.Variant), c.Totals.Baseline)
		writeSideTotals(out, c.Candidate.Variant, c.Totals.Candidate)
	}
}

func writeVariantHeading(out *strings.Builder, cs []*Comparison) {
	fmt.Fprintf(out, "  Item | %s", cs[0].Baseline.Variant)
	for _, c := range cs {
		fmt.Fprintf(out, " | %s", c.Candidate.Variant)
	}
	out.WriteByte('\n')
}

func writeVariantRow(out *strings.Builder, cs []*Comparison, pair Pair) {
	fmt.Fprintf(out, "  %s/%s", pair.Scenario, pair.Item)
	var baseline *GroupDelta
	for _, c := range cs {
		if g := findDelta(c, pair); g != nil {
			baseline = g
			break
		}
	}
	if baseline.Baseline.Probes > 0 {
		out.WriteString(" | quality not_assessed")
	} else {
		fmt.Fprintf(out, " | %d/%d passed", baseline.Baseline.Passed, baseline.Baseline.Ran)
	}
	for _, c := range cs {
		g := findDelta(c, pair)
		if g == nil {
			out.WriteString(" | absent")
			continue
		}
		if g.Candidate.Probes > 0 {
			fmt.Fprintf(out, " | quality not_assessed %s", g.Status)
			continue
		}
		fmt.Fprintf(out, " | %d/%d %s%s", g.Candidate.Passed, g.Candidate.Ran, g.Status, judgmentFlags(g.Uncalibrated, g.TruncatedJudgments, g.Judgments))
	}
	out.WriteByte('\n')
	for _, c := range cs {
		if g := findDelta(c, pair); g != nil && g.Status != statusUnchanged {
			fmt.Fprintf(out, "    [%s]\n", c.Candidate.Variant)
			writeGroupDetails(out, *g)
		}
	}
}

func variantPairStatus(cs []*Comparison, pair Pair) string {
	for _, section := range comparisonSections() {
		for _, c := range cs {
			if g := findDelta(c, pair); g != nil && g.Status == section.status {
				return section.status
			}
		}
	}
	return statusUnchanged
}

func findDelta(c *Comparison, pair Pair) *GroupDelta {
	for i := range c.Groups {
		if c.Groups[i].Scenario == pair.Scenario && c.Groups[i].Item == pair.Item {
			return &c.Groups[i]
		}
	}
	return nil
}
