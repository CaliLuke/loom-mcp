package control

import (
	"context"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type comparisonTrial struct {
	record JobRecord
	side   eval.Side
}

type comparisonSelection struct {
	sides []eval.Side
	jobs  map[eval.Pair][]ComparisonJob
	probe bool
}

// sameComparisonCandidate compares installation contracts, not candidate model settings.
func sameComparisonCandidate(a, b comparisonTrial) bool {
	x, y := a.record.Submission, b.record.Submission
	if x.Candidate != y.Candidate || x.Kind != y.Kind {
		return false
	}
	return sameComparisonInstallation(a, b)
}

func sameComparisonInstallation(a, b comparisonTrial) bool {
	if a.record.Submission.HostBinding != b.record.Submission.HostBinding {
		return false
	}
	ap, bp := a.side.Experiment.Attempts[0].Prepared, b.side.Experiment.Attempts[0].Prepared
	if ap.SurfaceID != bp.SurfaceID || ap.SurfaceVersion != bp.SurfaceVersion || len(ap.Bindings) != len(bp.Bindings) {
		return false
	}
	for _, binding := range ap.Bindings {
		if !slices.Contains(bp.Bindings, binding) {
			return false
		}
	}
	return true
}

// compatibleComparisonCase allows different cases to bind different tasks/plans.
func compatibleComparisonCase(first, next comparisonTrial) bool {
	a, b := first.record.Submission, next.record.Submission
	if a.TaskID != b.TaskID || a.TaskSnapshot != b.TaskSnapshot || !sameComparisonInstallation(first, next) {
		return false
	}
	return a.Kind != eval.ExecutionEvaluation || b.Kind != eval.ExecutionEvaluation || a.EvaluationPlan == b.EvaluationPlan
}

// compareGroups validates all original associations before producing a safe projection.
func (s *Service) compareGroups(ctx context.Context, q CompareRequest) (Comparison, error) {
	seen := make(map[string]bool)
	cases := make(map[eval.Pair]comparisonTrial)
	base, err := s.comparisonSelection(ctx, q.BaselineJobIDs, seen, cases)
	if err != nil {
		return Comparison{}, err
	}
	cand, err := s.comparisonSelection(ctx, q.CandidateJobIDs, seen, cases)
	if err != nil {
		return Comparison{}, err
	}
	compared, err := eval.CompareTrials(base.sides, cand.sides)
	if err != nil {
		return Comparison{}, publicError(err, InvalidInput, "")
	}
	quality := !base.probe && !cand.probe
	out := Comparison{Status: "comparable", Baseline: comparisonStats(compared.Totals.Baseline, quality), Candidate: comparisonStats(compared.Totals.Candidate, quality)}
	if !quality || len(compared.Groups) == 0 {
		out.Status = "not_comparable"
	}
	for _, g := range compared.Groups {
		pair := eval.Pair{Scenario: g.Scenario, Item: g.Item}
		out.Groups = append(out.Groups, ComparisonGroup{BaselineJobs: base.jobs[pair], CandidateJobs: cand.jobs[pair], Baseline: comparisonStats(g.Baseline, quality), Candidate: comparisonStats(g.Candidate, quality)})
	}
	for _, pair := range compared.OnlyBaseline {
		out.OnlyBaseline = append(out.OnlyBaseline, base.jobs[pair]...)
	}
	for _, pair := range compared.OnlyCandidate {
		out.OnlyCandidate = append(out.OnlyCandidate, cand.jobs[pair]...)
	}
	for _, warning := range compared.Warnings {
		if !slices.Contains(out.WarningCodes, warning.Code) {
			out.WarningCodes = append(out.WarningCodes, warning.Code)
		}
	}
	return out, nil
}

// comparisonSelection enforces one candidate/kind per side and exact case bindings.
func (s *Service) comparisonSelection(ctx context.Context, ids []string, seen map[string]bool, cases map[eval.Pair]comparisonTrial) (comparisonSelection, error) {
	out := comparisonSelection{jobs: make(map[eval.Pair][]ComparisonJob)}
	var first comparisonTrial
	for i, id := range ids {
		if seen[id] {
			return comparisonSelection{}, invalid("")
		}
		seen[id] = true
		trial, err := s.comparisonTrial(ctx, id, true)
		if err != nil {
			return comparisonSelection{}, err
		}
		if i == 0 {
			first = trial
		} else if !sameComparisonCandidate(first, trial) {
			return comparisonSelection{}, NewError(Unsupported, "", "", "")
		}
		r := trial.record
		pair := eval.Pair{Scenario: r.Attempt.Scenario, Item: r.Attempt.Item}
		if previous, ok := cases[pair]; ok && !compatibleComparisonCase(previous, trial) {
			return comparisonSelection{}, NewError(Unsupported, "", "", "")
		}
		// Retain the evaluation plan if this case first appeared as a probe.
		if previous, ok := cases[pair]; !ok || previous.record.Submission.Kind == eval.ExecutionProbe {
			cases[pair] = trial
		}
		out.sides = append(out.sides, trial.side)
		out.jobs[pair] = append(out.jobs[pair], ComparisonJob{JobID: id, State: r.State})
	}
	out.probe = first.record.Submission.Kind == eval.ExecutionProbe
	return out, nil
}
