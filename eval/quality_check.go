package eval

// QualityCounts tallies stored attempt verdicts for a quality check.
type QualityCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Error   int `json:"error"`
	Pending int `json:"pending"`
	NotRun  int `json:"not_run"`
}

// QualityCheck is an offline decision over one stored experiment. Reasons are
// stable codes so callers can inspect a rejection without parsing prose.
type QualityCheck struct {
	Policy   string        `json:"policy"`
	Passed   bool          `json:"passed"`
	Status   string        `json:"status"`
	Planned  int           `json:"planned"`
	Attempts int           `json:"attempts"`
	Counts   QualityCounts `json:"counts"`
	Reasons  []string      `json:"reasons"`
}

// CheckQualityStrict decides whether stored results meet the strict policy.
// It is pure: it does not read storage, rerun attempts, or invoke a judge.
// A pass requires a complete experiment, a nonempty contiguous plan, an intact
// attempts file, and a passed verdict for every attempt.
func CheckQualityStrict(e *Experiment) QualityCheck {
	result := QualityCheck{Policy: "strict", Reasons: []string{}}
	if e == nil {
		result.Reasons = []string{"no_experiment"}
		return result
	}
	result.Status = e.Header.Status
	result.Planned = e.Header.Planned
	result.Attempts = len(e.Attempts)
	if e.Header.Status != statusComplete {
		result.Reasons = append(result.Reasons, "incomplete_status")
	}
	if len(e.Attempts) == 0 {
		result.Reasons = append(result.Reasons, "no_attempts")
	}
	if e.TruncatedTail {
		result.Reasons = append(result.Reasons, "truncated_tail")
	}
	var coverage bool
	result.Counts, coverage = countQuality(e.Attempts, e.Header.Planned)
	if !coverage {
		result.Reasons = append(result.Reasons, "incomplete_plan")
	}
	result.Reasons = append(result.Reasons, verdictReasons(result.Counts)...)
	result.Passed = len(result.Reasons) == 0
	return result
}

// countQuality also checks the exact index range, so equal counts with a gap
// cannot satisfy the recorded plan.
func countQuality(attempts []*StoredAttempt, planned int) (QualityCounts, bool) {
	var counts QualityCounts
	coverage := planned > 0 && len(attempts) == planned
	seen := make(map[int]bool, len(attempts))
	for _, attempt := range attempts {
		if attempt == nil {
			coverage = false
			continue
		}
		if attempt.Index < 0 || attempt.Index >= planned || seen[attempt.Index] {
			coverage = false
		} else {
			seen[attempt.Index] = true
		}
		switch attempt.Verdict {
		case VerdictPassed:
			counts.Passed++
		case VerdictFailed:
			counts.Failed++
		case VerdictError:
			counts.Error++
		case VerdictPending:
			counts.Pending++
		case VerdictNotRun:
			counts.NotRun++
		default:
			coverage = false
		}
	}
	return counts, coverage
}

func verdictReasons(counts QualityCounts) []string {
	var reasons []string
	if counts.Failed > 0 {
		reasons = append(reasons, "failed_verdict")
	}
	if counts.Error > 0 {
		reasons = append(reasons, "error_verdict")
	}
	if counts.Pending > 0 {
		reasons = append(reasons, "pending_verdict")
	}
	if counts.NotRun > 0 {
		reasons = append(reasons, "not_run_verdict")
	}
	return reasons
}
