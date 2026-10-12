package suite

import (
	"math"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
)

// Diagnostics contains informational, never gated, per-scenario costs and events.
type Diagnostics struct {
	Attempts         int                `json:"attempts"`
	MedianSeconds    float64            `json:"median_seconds"`
	P90Seconds       float64            `json:"p90_seconds"`
	PlannerTokens    int                `json:"planner_tokens"`
	SummariserTokens int                `json:"summariser_tokens"`
	Scores           map[string]float64 `json:"scores"`
}

// Describe counts every completed call, including abandoned attempts.
func Describe(attempts []eval.StoredAttempt) (map[string]Diagnostics, error) {
	out := map[string]Diagnostics{}
	durations := map[string][]float64{}
	for _, a := range attempts {
		key := a.Variant + "/" + a.Scenario
		d := out[key]
		d.Attempts++
		if d.Scores == nil {
			d.Scores = map[string]float64{}
		}
		for _, s := range a.Scores {
			d.Scores[s.Name] += s.Value
		}
		durations[key] = append(durations[key], a.Duration.Seconds())
		for _, r := range a.Runs {
			rec, err := r.Record()
			if err != nil {
				return nil, err
			}
			for _, run := range rec.Runs {
				for _, c := range run.Calls {
					if c.Completion == nil {
						continue
					}
					u := c.Completion.Usage
					tokens := u.TotalTokens
					if tokens == 0 {
						tokens = u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
					}
					if c.Activity == callrecord.ActivitySummarize {
						d.SummariserTokens += tokens
					} else {
						d.PlannerTokens += tokens
					}
				}
			}
		}
		out[key] = d
	}
	for key, values := range durations {
		slices.Sort(values)
		d := out[key]
		d.MedianSeconds = values[len(values)/2]
		if len(values)%2 == 0 {
			d.MedianSeconds = (values[len(values)/2-1] + values[len(values)/2]) / 2
		}
		d.P90Seconds = values[int(math.Ceil(.9*float64(len(values))))-1]
		out[key] = d
	}
	return out, nil
}
