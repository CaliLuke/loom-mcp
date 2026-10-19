package eval

import (
	"cmp"
	"slices"
	"time"
)

// GroupStats counts stored verdicts; medians include only attempts that ran.
type GroupStats struct {
	Quality          string        `json:"quality,omitempty"`
	Probes           int           `json:"probes,omitempty,omitzero"`
	Unassessed       int           `json:"unassessed,omitempty,omitzero"`
	Attempts         int           `json:"attempts"`
	Ran              int           `json:"ran"`
	Passed           int           `json:"passed"`
	Failed           int           `json:"failed"`
	Errored          int           `json:"errored"`
	Pending          int           `json:"pending"`
	NotRun           int           `json:"not_run"`
	MedianTokens     int           `json:"median_tokens"`
	MedianDuration   time.Duration `json:"median_duration"`
	ToolsUnavailable int           `json:"tools_unavailable"`
}

type reportGroup struct {
	Variant  string `json:"variant"`
	Scenario string `json:"scenario"`
	Item     string `json:"item"`
	GroupStats
	attempts []*StoredAttempt
}

// reportGroups restores declaration order from attempt indices, not arrival order.
func reportGroups(attempts []*StoredAttempt) []reportGroup {
	ordered := slices.Clone(attempts)
	slices.SortStableFunc(ordered, func(a, b *StoredAttempt) int {
		return cmp.Compare(a.Index, b.Index)
	})
	positions := make(map[[3]string]int)
	var groups []reportGroup
	for _, a := range ordered {
		key := [3]string{a.Variant, a.Scenario, a.Item}
		pos, ok := positions[key]
		if !ok {
			pos = len(groups)
			positions[key] = pos
			groups = append(groups, reportGroup{Variant: a.Variant, Scenario: a.Scenario, Item: a.Item})
		}
		groups[pos].attempts = append(groups[pos].attempts, a)
	}
	for i := range groups {
		groups[i].GroupStats = groupStats(groups[i].attempts)
	}
	return groups
}

func groupStats(attempts []*StoredAttempt) GroupStats {
	stats := GroupStats{Attempts: len(attempts)}
	var tokens []int
	var durations []time.Duration
	for _, a := range attempts {
		if a.ExecutionKind == ExecutionProbe {
			stats.Probes++
			stats.Quality = probeQualityNotAssessed
		}
		switch a.Verdict {
		case VerdictUnassessed:
			stats.Unassessed++
		case VerdictPassed:
			stats.Passed++
		case VerdictFailed:
			stats.Failed++
		case VerdictError:
			stats.Errored++
		case VerdictPending:
			stats.Pending++
		case VerdictNotRun:
			stats.NotRun++
		}
		if a.Verdict != VerdictNotRun {
			tokens = append(tokens, a.Tokens)
			durations = append(durations, a.Duration)
		}
	}
	stats.Ran = stats.Attempts - stats.NotRun
	stats.MedianTokens, stats.MedianDuration = medianInteger(tokens), medianInteger(durations)
	return stats
}
