package summaryeval

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// ShowJSON returns the versioned compaction show document.
func ShowJSON(e *Experiment) ([]byte, error) {
	return encode(struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		*Experiment
		Aggregates []VariantAggregate `json:"aggregates"`
	}{"loom-mcp.eval.compaction.show", 1, e, Aggregate(e.Lines)})
}

// Show writes cut evidence and summaries, abbreviated to twenty lines unless full.
func Show(w io.Writer, e *Experiment, full bool) error {
	var out strings.Builder
	fmt.Fprintf(&out, "Compaction replay %s (%s): %d cuts, %d tokens, %d calls\n", e.Header.ID, e.Header.Status, len(e.Lines), e.Header.Tokens, e.Header.Calls)
	for _, a := range Aggregate(e.Lines) {
		fmt.Fprintf(&out, "Variant %s: %d verified comparisons, retention %.3f -> %.3f; better=%d worse=%d same=%d\n", a.Variant, a.Cuts, a.Old.Retention, a.New.Retention, a.Better, a.Worse, a.Same)
		fmt.Fprintf(&out, "  bytes %d -> %d; tokens=%d chunks=%d latency=%s\n", a.OldBytes, a.NewBytes, a.Tokens, a.Chunks, a.Latency)
		for _, category := range slices.Sorted(maps.Keys(a.Old.Categories)) {
			fmt.Fprintf(&out, "  %s: %.3f -> %.3f\n", category, a.Old.Categories[category].Retention, a.New.Categories[category].Retention)
		}
	}
	for _, line := range e.Lines {
		fmt.Fprintf(&out, "\n%s — %s %s messages %d-%d", line.Key, line.Verification.Status, line.Cut.Origin, line.Cut.From, line.Cut.To)
		if line.Verification.Reason != "" {
			fmt.Fprintf(&out, " (%s)", line.Verification.Reason)
		}
		if line.Verification.Field != "" {
			fmt.Fprintf(&out, " (call %d: %s)", line.Verification.FirstCall, line.Verification.Field)
		}
		fmt.Fprintf(&out, "\nnot_run=%s error=%s\n", line.NotRun, line.Error)
		writeCutRetention(&out, line)
		for _, side := range []struct {
			name  string
			value Side
		}{{sideOld, line.Old}, {sideNew, line.New}} {
			compression := float64(0)
			if side.value.Bytes > 0 {
				compression = float64(line.Cut.EvictedBytes) / float64(side.value.Bytes)
			}
			fmt.Fprintf(&out, "%s: kind=%q bytes=%d compression=%.2fx rendered=%d tokens=%d chunks=%d latency=%s cached=%t\n", side.name, side.value.Kind, side.value.Bytes, compression, side.value.RenderedBytes, tokenCount(side.value.Usage), side.value.Chunks, side.value.Latency, side.value.Cached)
			lines := strings.Split(side.value.Text, "\n")
			if !full && len(lines) > 20 {
				lines = append(lines[:20], "… (use --full)")
			}
			fmt.Fprintf(&out, "%s\n", strings.Join(lines, "\n"))
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

// ListJSON returns the versioned compaction listing document.
func ListJSON(headers []Header) ([]byte, error) {
	return encode(struct {
		Format  string   `json:"format"`
		Version int      `json:"version"`
		Replays []Header `json:"replays"`
	}{"loom-mcp.eval.compaction.list", 1, headers})
}

func writeCutRetention(out *strings.Builder, line Line) {
	old, next := SideRetention{Categories: map[string]Retention{}}, SideRetention{Categories: map[string]Retention{}}
	for _, judgment := range line.Judgments {
		if judgment.Side == sideOld {
			addRetention(&old, judgment)
		} else {
			addRetention(&next, judgment)
		}
	}
	for _, category := range slices.Sorted(maps.Keys(old.Categories)) {
		fmt.Fprintf(out, "retention %s: %.3f -> %.3f\n", category, old.Categories[category].Retention, next.Categories[category].Retention)
	}
}
