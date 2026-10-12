package summaryeval

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

type judgmentSide struct {
	judgments []Judgment
	usage     model.TokenUsage
	calls     int
}

type comparison struct {
	input judge.Input
	facts []Fact
	old   *judgmentSide
	err   error
}

type cutLocator struct {
	run  *runrecord.Run
	step int
}

const comparisonTask = "Evaluate whether the summary preserves the facts and intent in the reference, without inventing or changing them."

// Rubric returns the six built-in retention claims in stable order.
func Rubric() []Fact {
	return []Fact{
		{Claim: judge.Claim{ID: "identifiers", Text: "The summary preserves the important identifiers and names in the reference."}, Category: "identifiers"},
		{Claim: judge.Claim{ID: "numbers", Text: "The summary preserves the important numbers, quantities and dates in the reference."}, Category: "numbers"},
		{Claim: judge.Claim{ID: "decisions", Text: "The summary preserves the decisions and their reasons in the reference."}, Category: "decisions"},
		{Claim: judge.Claim{ID: "user_constraints", Text: "The summary preserves the user's requirements and constraints in the reference."}, Category: "user_constraints"},
		{Claim: judge.Claim{ID: "progress", Text: "The summary preserves completed work, unresolved questions and next steps in the reference."}, Category: "progress"},
		{Claim: judge.Claim{ID: "faithful", Text: "Every assertion in the summary is supported by the reference, without invented facts or contradictions."}, Category: "faithful"},
	}
}

func prepareComparison(ctx context.Context, cfg Config, task work) (*comparison, error) {
	facts := Rubric()
	if cfg.Facts != nil {
		extra, err := cfg.Facts(ctx, task.cut)
		if err != nil {
			return nil, err
		}
		facts = append(facts, extra...)
	}
	seen := map[string]bool{}
	for _, f := range facts {
		if f.Claim.ID == "" || f.Claim.Text == "" || f.Category == "" || seen[f.Claim.ID] {
			return nil, fmt.Errorf("compaction: facts require unique claim IDs, text and category")
		}
		seen[f.Claim.ID] = true
	}
	ev, err := evidence(task.cut, task.in)
	if err != nil {
		return nil, err
	}
	limit := cfg.ReferenceBytes
	if limit == 0 {
		limit = 384 << 10
	}
	if limit < 128 {
		return nil, fmt.Errorf("%w: ReferenceBytes must be at least 128", ErrUsage)
	}
	reference, truncated := boundedReference(ev, limit)
	return &comparison{facts: facts, input: judge.Input{Subject: originSummary, Task: comparisonTask, Reference: reference, Claims: claimList(facts), Truncated: truncated}}, nil
}

func claimList(facts []Fact) []judge.Claim {
	claims := make([]judge.Claim, len(facts))
	for i, f := range facts {
		claims[i] = f.Claim
	}
	return claims
}

func judgeSide(ctx context.Context, j *judge.Judge, input judge.Input, text, side string, facts []Fact) (judgmentSide, error) {
	input.Text = text
	result, err := j.Judge(ctx, input)
	out := judgmentSide{}
	if result == nil {
		return out, err
	}
	out.usage = result.Usage
	if !result.NoCall {
		out.calls = result.Corrections + 1
	}
	for i, judgment := range result.Judgments {
		out.judgments = append(out.judgments, Judgment{StoredJudgment: eval.StoredJudgment{ClaimID: judgment.ClaimID, Subject: originSummary, Label: string(judgment.Label), Rationale: judgment.Rationale, Uncalibrated: !result.Calibrated, Truncated: result.Truncated}, Side: side, Category: facts[i].Category, NoCall: result.NoCall})
	}
	return out, err
}

func applyJudgments(ctx context.Context, j *judge.Judge, c *comparison, line *Line, header *Header) error {
	if c.old == nil {
		old, err := judgeSide(ctx, j, c.input, line.Old.Text, sideOld, c.facts)
		c.old = &old
		c.err = err
		line.JudgeUsage = addUsage(line.JudgeUsage, old.usage)
		header.Tokens += tokenCount(old.usage)
		header.Calls += old.calls
	}
	line.Judgments = append(line.Judgments, c.old.judgments...)
	if c.err != nil {
		return c.err
	}
	next, err := judgeSide(ctx, j, c.input, line.New.Text, sideNew, c.facts)
	line.Judgments = append(line.Judgments, next.judgments...)
	if header.Judge != nil && len(next.judgments) > 0 {
		header.Judge.Calibrated = !next.judgments[0].Uncalibrated
	}
	line.JudgeUsage = addUsage(line.JudgeUsage, next.usage)
	header.Tokens += tokenCount(next.usage)
	header.Calls += next.calls
	return err
}

func addUsage(a, b model.TokenUsage) model.TokenUsage {
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.ModelClass == "" {
		a.ModelClass = b.ModelClass
	}
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.TotalTokens += b.TotalTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	return a
}

func cutKey(c Cut) cutLocator {
	return cutLocator{run: c.run, step: c.Step}
}

func prepareComparisons(ctx context.Context, cfg Config, opts Options, tasks []work, result *Experiment) (map[cutLocator]*comparison, error) {
	comparisons := map[cutLocator]*comparison{}
	if opts.Judge == nil {
		return comparisons, nil
	}
	result.Header.Judge = &eval.JudgeInfo{ModelID: opts.Judge.ModelID(), PromptHash: opts.Judge.PromptHash(), CalibrationKey: opts.Judge.CalibrationKey()}
	for _, task := range tasks {
		if task.in == nil || task.line.NotRun != "" {
			continue
		}
		key := cutKey(task.cut)
		if comparisons[key] == nil {
			baseline, err := Reconstruct(task.cut)
			if err != nil {
				return nil, err
			}
			original := task
			original.in = baseline
			c, err := prepareComparison(ctx, cfg, original)
			if err != nil {
				return nil, err
			}
			comparisons[key] = c
			result.Preflight.CallUpperBound += 4
		}
		result.Preflight.CallUpperBound += 4
	}
	return comparisons, nil
}
