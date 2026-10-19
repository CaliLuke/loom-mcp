package control

import (
	"context"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

// Submit admits a frozen candidate through the host's durable executor.
func (s *Service) Submit(ctx context.Context, q SubmitRequest) (Job, error) {
	if err := s.authorizeRequest(ctx, SubmitOperation, q); err != nil {
		return Job{}, err
	}
	sub, err := s.prepareSubmission(ctx, q)
	if err != nil {
		return Job{}, err
	}
	record, err := s.cfg.Executor.Accept(ctx, sub)
	if err != nil {
		return Job{}, publicError(err, OperationFailed, "")
	}
	if record.Submission != sub || !validRecord(record, s.cfg.Scope, record.ID) {
		return Job{}, NewError(OperationFailed, "", "", "")
	}
	return projectJob(record), nil
}

// Status loads a job within the service's fixed authorized host scope.
func (s *Service) Status(ctx context.Context, q JobRequest) (Job, error) {
	if err := s.authorizeRequest(ctx, StatusOperation, q); err != nil {
		return Job{}, err
	}
	r, err := s.job(ctx, q.JobID)
	if err != nil {
		return Job{}, err
	}
	return projectJob(r), nil
}

// Cancel requests host-owned cancellation without asserting stopped execution.
func (s *Service) Cancel(ctx context.Context, q JobRequest) (Job, error) {
	if err := s.authorizeRequest(ctx, CancelOperation, q); err != nil {
		return Job{}, err
	}
	before, err := s.job(ctx, q.JobID)
	if err != nil {
		return Job{}, err
	}
	if terminal(before.State) {
		return projectJob(before), nil
	}
	after, err := s.cfg.Executor.Cancel(ctx, s.cfg.Scope, q.JobID)
	if err != nil {
		return Job{}, publicError(err, OperationFailed, "")
	}
	if !validRecord(after, s.cfg.Scope, q.JobID) || before.Submission != after.Submission {
		return Job{}, NewError(OperationFailed, "", "", "")
	}
	return projectJob(after), nil
}

func validRecord(r JobRecord, scope, id string) bool {
	return identity(id) && r.ID == id && r.Submission.Scope == scope && validState(r.State)
}

func validState(state JobState) bool {
	switch state {
	case Queued, Running, CancelRequested, Succeeded, Failed, Canceled, Interrupted:
		return true
	default:
		return false
	}
}

func terminal(state JobState) bool {
	return state == Succeeded || state == Failed || state == Canceled || state == Interrupted
}

func projectJob(r JobRecord) Job {
	out := Job{ID: r.ID, State: r.State, EvidenceAvailable: r.Manifest != ""}
	if r.Failure != nil {
		out.Failure = NewError(r.Failure.Code, "", "", r.Failure.Field)
	}
	return out
}

func (s *Service) job(ctx context.Context, id string) (JobRecord, error) {
	if !identity(id) {
		return JobRecord{}, invalid(jsonJobId)
	}
	r, err := s.cfg.Executor.Status(ctx, s.cfg.Scope, id)
	if err != nil {
		return JobRecord{}, publicError(err, OperationFailed, "")
	}
	if !validRecord(r, s.cfg.Scope, id) {
		return JobRecord{}, NewError(NotFound, "", "", "")
	}
	return r, nil
}

// prepareSubmission resolves host-owned task facts before executor admission.
func (s *Service) prepareSubmission(ctx context.Context, q SubmitRequest) (Submission, error) {
	if !identity(q.RequestKey) {
		return Submission{}, invalid("request_key")
	}
	if !digest(q.Candidate, 64) {
		return Submission{}, invalid(jsonCandidate)
	}
	f, err := s.cfg.Candidates.Load(ctx, q.Candidate)
	if err != nil {
		return Submission{}, publicError(err, InvalidInput, jsonCandidate)
	}
	task, ok := s.tasks[q.TaskID]
	if !ok {
		return Submission{}, NewError(NotFound, "", "", "task_id")
	}
	kind := q.Kind
	if kind == "" {
		kind = eval.ExecutionProbe
	}
	if !slices.Contains(task.Task.Modes, kind) {
		return Submission{}, invalid(jsonKind)
	}
	if (kind == eval.ExecutionProbe && q.EvaluationPlan != "") || (kind == eval.ExecutionEvaluation && !slices.Contains(task.Task.EvaluationPlans, q.EvaluationPlan)) {
		return Submission{}, invalid(jsonEvaluationPlan)
	}
	if !bindingsEqual(f.Manifest().Bindings, task.ComponentBindings) {
		return Submission{}, NewError(Unsupported, "", "", jsonSlot)
	}
	return Submission{Scope: s.cfg.Scope, RequestKey: q.RequestKey, Candidate: f.Digest(), TaskID: q.TaskID, TaskSnapshot: task.Snapshot, Kind: kind, EvaluationPlan: q.EvaluationPlan, HostBinding: task.HostBinding}, nil
}
