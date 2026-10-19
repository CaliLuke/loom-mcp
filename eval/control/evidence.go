package control

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// ListEvidence creates a fresh exact job-bound view and pages authorized entries.
func (s *Service) ListEvidence(ctx context.Context, q EvidenceListRequest) (inspect.Page, error) {
	if err := s.authorizeRequest(ctx, ListEvidenceOperation, q); err != nil {
		return inspect.Page{}, err
	}
	if q.Offset < 0 || q.Limit < 0 {
		return inspect.Page{}, invalid(jsonOffset)
	}
	view, err := s.view(ctx, q.JobID)
	if err != nil {
		return inspect.Page{}, err
	}
	out, err := view.List(ctx, inspect.ListQuery{Offset: q.Offset, Limit: bounded(q.Limit, s.cfg.Limits.MaxPageEntries)})
	return out, publicError(err, OperationFailed, "")
}

// ReadEvidence reads bounded bytes through a newly bound authorized job view.
func (s *Service) ReadEvidence(ctx context.Context, q EvidenceReadRequest) (inspect.ReadResult, error) {
	if err := s.authorizeRequest(ctx, ReadEvidenceOperation, q); err != nil {
		return inspect.ReadResult{}, err
	}
	if q.Offset < 0 || q.MaxBytes < 0 {
		return inspect.ReadResult{}, invalid(jsonOffset)
	}
	if !digest(q.Locator, 64) {
		return inspect.ReadResult{}, NewError(NotFound, "", "", jsonLocator)
	}
	view, err := s.view(ctx, q.JobID)
	if err != nil {
		return inspect.ReadResult{}, err
	}
	out, err := view.Read(ctx, inspect.ReadQuery{Locator: q.Locator, Offset: q.Offset, MaxBytes: bounded(q.MaxBytes, s.cfg.Limits.MaxReadBytes)})
	return out, publicError(err, OperationFailed, "")
}
func (s *Service) view(ctx context.Context, id string) (*inspect.View, error) {
	r, err := s.job(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Manifest == "" {
		return nil, NewError(EvidenceNotReady, "", "", "")
	}
	v, err := s.cfg.Evidence(ctx, id)
	if err != nil {
		return nil, publicError(err, OperationFailed, "")
	}
	if v == nil {
		return nil, NewError(OperationFailed, "", "", "")
	}
	return v, nil
}
