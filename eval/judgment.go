package eval

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Label describes how the evidence supports a claim.
type Label string

// JudgedStep locates a unit by zero-based session run and trajectory step index.
type JudgedStep struct {
	Run, Index int
	Tool       tools.Ident
	CallID     string
}

// Judgment retains one claim's result, or a batch infrastructure error.
// Step claims have one judgment per matching unit, or one synthetic missing unit.
type Judgment struct {
	ClaimID                                string
	Subject                                Subject
	Step                                   *JudgedStep
	Label                                  Label
	Rationale                              string
	Informational, Uncalibrated, Truncated bool
	Err                                    error
}

// OutcomeJudge adapts an external judge without coupling eval to eval/judge.
// Implementations must support concurrent JudgeOutcome calls and honor context.
type OutcomeJudge interface {
	Prepare(ctx context.Context) error
	JudgeOutcome(ctx context.Context, out *Outcome) (JudgeResult, error)
	Info() JudgeInfo
}

// JudgeResult preserves claim order, then unit order, and excludes calibration usage.
type JudgeResult struct {
	Judgments []Judgment
	Usage     model.TokenUsage
	Tokens    int
}

const (
	// Entailed means the evidence supports the claim.
	Entailed Label = "entailed"
	// Contradicted means the evidence opposes the claim.
	Contradicted Label = "contradicted"
	// NotAddressed means the evidence does not address the claim.
	NotAddressed Label = "not_addressed"
	// Indeterminate means the evidence cannot resolve the claim.
	Indeterminate Label = "indeterminate"
)

// claimVerdict applies error and failure precedence across every gating unit.
func claimVerdict(claims []Claim, judgments []Judgment) Verdict {
	verdict := VerdictPassed
	for _, c := range claims {
		if c.Informational {
			continue
		}
		found := false
		for _, j := range judgments {
			if j.ClaimID != c.ID {
				continue
			}
			found = true
			if j.Err != nil {
				return VerdictError
			}
			if j.Label != Entailed {
				verdict = VerdictFailed
			}
		}
		if !found && verdict == VerdictPassed {
			verdict = VerdictPending
		}
	}
	return verdict
}

// judgeAttempt runs only after runAttempt has closed the harness and fixed Duration.
func judgeAttempt(ctx context.Context, judge OutcomeJudge, timeout time.Duration, r *AttemptRecord) {
	if judge == nil || r.NotRun != "" || r.HarnessErr != nil || r.Outcome == nil || r.Outcome.Err != nil || len(r.Outcome.Claims) == 0 {
		return
	}
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := judge.JudgeOutcome(ctx, r.Outcome)
	r.Outcome.Judgments = result.Judgments
	r.JudgeUsage, r.JudgeTokens = result.Usage, result.Tokens
	if err != nil {
		r.Outcome.Err = err
	}
}
