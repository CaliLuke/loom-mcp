// Package judging adapts eval outcomes to the rendered-text judge.
package judging

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// Config selects a judge and rendering limits. Zero Limits uses DefaultLimits.
type Config struct {
	Judge  *judge.Judge
	Limits trajectory.Limits
}

// Judging renders subjects and maps judge responses back to ordered eval claims.
// It is safe for concurrent use; callers must not mutate outcomes during judging.
type Judging struct {
	judge      *judge.Judge
	limits     trajectory.Limits
	calibrated atomic.Bool
}

type batch struct {
	subject eval.Subject
	claims  []eval.Claim
	step    *eval.JudgedStep
}

// New validates the judge and fixes rendering limits.
func New(cfg Config) (*Judging, error) {
	if cfg.Judge == nil {
		return nil, errors.New("judging: nil judge")
	}
	if cfg.Limits == (trajectory.Limits{}) {
		cfg.Limits = trajectory.DefaultLimits()
	}
	return &Judging{judge: cfg.Judge, limits: cfg.Limits}, nil
}

// Prepare calibrates before runner workers receive attempts. Calibration failures
// are non-fatal to judging and are visible through Info and judgment flags.
func (j *Judging) Prepare(ctx context.Context) error {
	c, err := j.judge.Calibrate(ctx)
	j.calibrated.Store(err == nil && c != nil && c.Passed)
	return err
}

// JudgeOutcome judges batches sequentially and returns results in claim/unit order.
// Rendering and provider failures become judgments; invalid arguments return errors.
func (j *Judging) JudgeOutcome(ctx context.Context, out *eval.Outcome) (eval.JudgeResult, error) {
	result := eval.JudgeResult{}
	if out == nil || out.Session == nil {
		return result, errors.New("judging: outcome and session are required")
	}
	for _, claim := range out.Claims {
		switch claim.Subject {
		case "", eval.SubjectAnswer, eval.SubjectTrajectory, eval.SubjectSession, eval.SubjectStep:
		default:
			return result, fmt.Errorf("judging: unknown subject %q", claim.Subject)
		}
	}
	byClaim := make(map[string][]eval.Judgment, len(out.Claims))
	for _, b := range batches(out) {
		judgments, usage := j.judgeBatch(ctx, out, b)
		for _, judgment := range judgments {
			byClaim[judgment.ClaimID] = append(byClaim[judgment.ClaimID], judgment)
		}
		result.Usage.InputTokens += usage.Usage.InputTokens
		result.Usage.OutputTokens += usage.Usage.OutputTokens
		result.Usage.CacheReadTokens += usage.Usage.CacheReadTokens
		result.Usage.CacheWriteTokens += usage.Usage.CacheWriteTokens
		result.Usage.TotalTokens += usage.Usage.TotalTokens
		result.Tokens += usage.Tokens
	}
	for _, claim := range out.Claims {
		result.Judgments = append(result.Judgments, byClaim[claim.ID]...)
	}
	return result, nil
}

// Info reports judge provenance and the most recently observed calibration status.
func (j *Judging) Info() eval.JudgeInfo {
	return eval.JudgeInfo{ModelID: j.judge.ModelID(), PromptHash: j.judge.PromptHash(), CalibrationKey: j.judge.CalibrationKey(), Calibrated: j.calibrated.Load()}
}

func batches(out *eval.Outcome) []batch {
	var result []batch
	for _, subject := range []eval.Subject{eval.SubjectAnswer, eval.SubjectTrajectory, eval.SubjectSession} {
		b := batch{subject: subject}
		for _, c := range out.Claims {
			if c.Subject == subject || c.Subject == "" && subject == eval.SubjectAnswer {
				b.claims = append(b.claims, c)
			}
		}
		if len(b.claims) > 0 {
			result = append(result, b)
		}
	}
	seen := make(map[tools.Ident]bool)
	for _, c := range out.Claims {
		if c.Subject != eval.SubjectStep || seen[c.Tool] {
			continue
		}
		seen[c.Tool] = true
		b := batch{subject: eval.SubjectStep}
		for _, claim := range out.Claims {
			if claim.Subject == eval.SubjectStep && claim.Tool == c.Tool {
				b.claims = append(b.claims, claim)
			}
		}
		units := steps(out.Session.Trajectory, c.Tool)
		if len(units) == 0 {
			result = append(result, b)
		}
		for _, unit := range units {
			b.step = &unit
			result = append(result, b)
		}
	}
	return result
}

// batchJudgments preserves claim metadata while mapping one provider result or error.
func batchJudgments(b batch, result *judge.Result, truncated, calibrated bool, err error) []eval.Judgment {
	judgments := make([]eval.Judgment, len(b.claims))
	for i, c := range b.claims {
		judgments[i] = eval.Judgment{ClaimID: c.ID, Subject: b.subject, Step: b.step,
			Informational: c.Informational, Truncated: truncated, Uncalibrated: !calibrated, Err: err}
		if result != nil {
			judgments[i].Uncalibrated = !result.NoCall && !result.Calibrated
			if err == nil {
				judgments[i].Label = eval.Label(result.Judgments[i].Label)
				judgments[i].Rationale = result.Judgments[i].Rationale
			}
		}
	}
	return judgments
}

// judgmentUsage uses positive TotalTokens, otherwise the disjoint component sum.
func judgmentUsage(result *judge.Result) eval.JudgeResult {
	if result == nil {
		return eval.JudgeResult{}
	}
	usage := eval.JudgeResult{Usage: result.Usage, Tokens: result.Usage.TotalTokens}
	if usage.Tokens <= 0 {
		usage.Tokens = result.Usage.InputTokens + result.Usage.OutputTokens + result.Usage.CacheReadTokens + result.Usage.CacheWriteTokens
	}
	return usage
}

func (j *Judging) judgeBatch(ctx context.Context, out *eval.Outcome, b batch) ([]eval.Judgment, eval.JudgeResult) {
	if b.subject == eval.SubjectStep && b.step == nil {
		judgments := batchJudgments(b, nil, false, true, nil)
		for i := range judgments {
			judgments[i].Label = eval.NotAddressed
			judgments[i].Rationale = "no matching step"
		}
		return judgments, eval.JudgeResult{}
	}
	text, truncated, err := render(out, b, j.limits)
	in := judge.Input{Subject: string(b.subject), Text: text, Truncated: truncated, Task: out.Task, Reference: out.Reference}
	for _, c := range b.claims {
		in.Claims = append(in.Claims, judge.Claim{ID: c.ID, Text: c.Text})
	}
	var result *judge.Result
	if err == nil {
		result, err = j.judge.Judge(ctx, in)
	}
	if err == nil && result != nil && !result.NoCall {
		j.calibrated.Store(result.Calibrated)
	}
	return batchJudgments(b, result, truncated, j.calibrated.Load(), err), judgmentUsage(result)
}
