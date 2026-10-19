package eval

// Verdict separates execution errors, quality failures and unjudged gating claims.
type Verdict string

const (
	// VerdictUnassessed means a successful probe has no quality assessment.
	VerdictUnassessed Verdict = "unassessed"
	// VerdictPassed means every quality gate passed and no gating claim is pending.
	VerdictPassed Verdict = "passed"
	// VerdictFailed means a check or score threshold failed.
	VerdictFailed Verdict = "failed"
	// VerdictError means execution or harness infrastructure failed.
	VerdictError Verdict = "error"
	// VerdictPending means a gating claim awaits judging.
	VerdictPending Verdict = "pending"
	// VerdictNotRun means the attempt never started.
	VerdictNotRun Verdict = "not_run"
)

// Verdict applies error, failed, pending, passed precedence.
func (o *Outcome) Verdict() Verdict {
	if o == nil || o.Err != nil {
		return VerdictError
	}
	claims := claimVerdict(o.Claims, o.Judgments)
	if claims == VerdictError {
		return claims
	}
	for _, c := range o.Checks {
		if !c.Passed {
			return VerdictFailed
		}
	}
	for _, s := range o.Scores {
		if s.Threshold != nil && s.Value < *s.Threshold {
			return VerdictFailed
		}
	}
	return claims
}

// Verdict gives not-run and harness errors precedence over the outcome.
func (r *AttemptRecord) Verdict() Verdict {
	if r.NotRun != "" {
		return VerdictNotRun
	}
	if r.HarnessErr != nil {
		return VerdictError
	}
	if r.ExecutionKind == ExecutionProbe {
		if r.Outcome == nil || r.Outcome.Err != nil || r.Probe == nil || r.Probe.Execution != probeExecutionSucceeded {
			return VerdictError
		}
		return VerdictUnassessed
	}
	return r.Outcome.Verdict()
}
