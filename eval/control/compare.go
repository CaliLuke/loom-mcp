package control

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

// Compare returns existing comparison statistics without protected annotations.
func (s *Service) Compare(ctx context.Context, q CompareRequest) (Comparison, error) {
	if err := s.authorizeRequest(ctx, CompareOperation, q); err != nil {
		return Comparison{}, err
	}
	base, err := s.comparisonSide(ctx, q.BaselineJobID)
	if err != nil {
		return Comparison{}, err
	}
	cand, err := s.comparisonSide(ctx, q.CandidateJobID)
	if err != nil {
		return Comparison{}, err
	}
	compared, err := eval.Compare(base, cand, eval.CompareOptions{})
	if err != nil {
		return Comparison{}, publicError(err, InvalidInput, "")
	}
	probe := base.Experiment.Header.ExecutionKind == eval.ExecutionProbe || cand.Experiment.Header.ExecutionKind == eval.ExecutionProbe
	out := Comparison{Status: "comparable", Baseline: comparisonStats(compared.Totals.Baseline, !probe), Candidate: comparisonStats(compared.Totals.Candidate, !probe)}
	if probe {
		out.Status = "not_comparable"
	}
	for _, w := range compared.Warnings {
		out.WarningCodes = append(out.WarningCodes, w.Code)
	}
	return out, nil
}

func comparisonStats(s eval.GroupStats, quality bool) ComparisonSide {
	out := ComparisonSide{Attempts: s.Attempts, Ran: s.Ran, MedianTokens: s.MedianTokens, MedianDurationNanos: int64(s.MedianDuration)}
	if quality {
		out.Quality = &QualityCounts{Passed: s.Passed, Failed: s.Failed, Errored: s.Errored, Pending: s.Pending, NotRun: s.NotRun}
	}
	return out
}

func (s *Service) comparisonSide(ctx context.Context, id string) (eval.Side, error) {
	r, err := s.job(ctx, id)
	if err != nil {
		return eval.Side{}, err
	}
	if !terminal(r.State) || r.ExperimentID == "" {
		return eval.Side{}, NewError(EvidenceNotReady, "", "", "")
	}
	e, err := s.cfg.Experiments.Load(r.ExperimentID)
	if err != nil {
		return eval.Side{}, publicError(err, OperationFailed, "")
	}
	if !e.Complete() || e.Header.ID != r.ExperimentID || len(e.Attempts) != 1 || e.Attempts[0].AttemptKey != r.Attempt || len(e.Header.Variants) != 1 || len(e.Header.Scenarios) != 1 || e.Header.Planned != 1 {
		return eval.Side{}, NewError(Unsupported, "", "", "")
	}
	a := e.Attempts[0]
	if a.Prepared == nil || a.Prepared.CandidateDigest != r.Submission.Candidate || a.Prepared.TaskSnapshot != r.Submission.TaskSnapshot || a.ExecutionKind != r.Submission.Kind {
		return eval.Side{}, NewError(Unsupported, "", "", "")
	}
	return eval.Side{Experiment: e, Variant: r.Attempt.Variant}, nil
}
