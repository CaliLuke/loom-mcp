package control

import (
	"context"
	"encoding/json/v2"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

// PreviewCopyRequest selects source component roots for a complete read-only preview.
type PreviewCopyRequest struct {
	DraftID          string   `json:"draft_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	SourceCandidate  string   `json:"source_candidate"`
	Components       []string `json:"components"`
}

// CopyRequest confirms the exact complete preview and every proposed replacement.
type CopyRequest struct {
	DraftID          string   `json:"draft_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	SourceCandidate  string   `json:"source_candidate"`
	Components       []string `json:"components"`
	Replace          []string `json:"replace"`
	PlanDigest       string   `json:"plan_digest"`
}

// CopyResult returns candidate metadata and immediate origins without component bytes.
type CopyResult struct {
	Candidate  CandidateInfo               `json:"candidate"`
	PlanDigest string                      `json:"plan_digest"`
	Selected   []string                    `json:"selected"`
	Closure    []string                    `json:"closure"`
	Origins    []candidate.ComponentOrigin `json:"origins"`
}

const (
	// PreviewCopyOperation enables complete dependency/collision previews without mutation.
	PreviewCopyOperation Operation = "preview_copy"
	// CopyOperation enables atomic import of an explicitly confirmed preview.
	CopyOperation Operation = "copy_candidate"
)

// PreviewCopy returns all selected source dependencies and destination collisions before mutation.
func (s *Service) PreviewCopy(ctx context.Context, q PreviewCopyRequest) (candidate.CopyPlan, error) {
	if err := s.authorizeCopy(ctx, PreviewCopyOperation, q); err != nil {
		return candidate.CopyPlan{}, err
	}
	plan, err := s.cfg.Candidates.PreviewCopy(ctx, q.DraftID, q.ExpectedRevision, candidate.PreviewCopyRequest{SourceCandidate: q.SourceCandidate, Components: q.Components})
	if err != nil {
		return candidate.CopyPlan{}, publicError(err, InvalidInput, jsonComponents)
	}
	if err := s.validateCopyPlan(plan); err != nil {
		return candidate.CopyPlan{}, err
	}
	return plan, nil
}

// Copy applies exactly the confirmed preview at the current destination revision.
func (s *Service) Copy(ctx context.Context, q CopyRequest) (CopyResult, error) {
	if err := s.authorizeCopy(ctx, CopyOperation, q); err != nil {
		return CopyResult{}, err
	}
	// Apply the same complete-response bound before mutation. The core repeats
	// the immutable-plan check under its own lock, closing the revision race.
	plan, err := s.cfg.Candidates.PreviewCopy(ctx, q.DraftID, q.ExpectedRevision, candidate.PreviewCopyRequest{SourceCandidate: q.SourceCandidate, Components: q.Components})
	if err != nil {
		return CopyResult{}, publicError(err, InvalidInput, jsonComponents)
	}
	if err := s.validateCopyPlan(plan); err != nil {
		return CopyResult{}, err
	}
	result, err := s.cfg.Candidates.Copy(ctx, q.DraftID, q.ExpectedRevision, candidate.CopyRequest{SourceCandidate: q.SourceCandidate, Components: q.Components, Replace: q.Replace, PlanDigest: q.PlanDigest})
	if err != nil {
		return CopyResult{}, publicError(err, InvalidInput, jsonComponents)
	}
	return CopyResult{Candidate: draftInfo(result.Draft), PlanDigest: result.PlanDigest, Selected: result.Selected, Closure: result.Closure, Origins: result.Draft.Origins}, nil
}

// authorizeCopy rechecks actual read/edit grants in the constructor-bound store.
func (s *Service) authorizeCopy(ctx context.Context, op Operation, q any) error {
	if err := s.authorizeRequest(ctx, op, q); err != nil {
		return err
	}
	for _, required := range []Operation{ReadOperation, EditOperation} {
		if err := s.authorize(ctx, required); err != nil {
			return err
		}
	}
	return nil
}

// validateCopyPlan rejects an oversized complete plan rather than authorizing a prefix.
func (s *Service) validateCopyPlan(plan candidate.CopyPlan) error {
	if len(plan.Closure) > s.cfg.Limits.MaxPageEntries {
		return invalid(jsonComponents)
	}
	for _, ref := range plan.Closure {
		if utf8.RuneCountInString(ref.Name) > 256 {
			return invalid(jsonComponents)
		}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return publicError(err, OperationFailed, "")
	}
	if len(data) > s.cfg.Limits.MaxReadBytes {
		return invalid(jsonComponents)
	}
	return nil
}
