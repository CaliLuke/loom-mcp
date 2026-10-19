package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

// Create starts a draft from an explicit authorized baseline or frozen candidate.
func (s *Service) Create(ctx context.Context, q CreateRequest) (CandidateInfo, error) {
	if err := s.authorizeRequest(ctx, CreateOperation, q); err != nil {
		return CandidateInfo{}, err
	}
	if (q.Baseline == "") == (q.SourceCandidate == "") {
		return CandidateInfo{}, invalid(jsonBaseline)
	}
	var d candidate.Draft
	var err error
	if q.Baseline != "" {
		if q.Baseline != s.cfg.Candidates.Discover().BaselineDigest {
			return CandidateInfo{}, NewError(NotFound, "", "", "")
		}
		d, err = s.cfg.Candidates.Create(ctx)
	} else {
		if !digest(q.SourceCandidate, 64) {
			return CandidateInfo{}, invalid(jsonSourceCandidate)
		}
		d, err = s.cfg.Candidates.Fork(ctx, q.SourceCandidate)
	}
	return draftInfo(d), publicError(err, InvalidInput, jsonContent)
}

// GetCandidate returns owned metadata and a current optimistic draft revision.
func (s *Service) GetCandidate(ctx context.Context, q GetCandidateRequest) (CandidateInfo, error) {
	if err := s.authorizeRequest(ctx, GetOperation, q); err != nil {
		return CandidateInfo{}, err
	}
	if (q.DraftID == "") == (q.Candidate == "") {
		return CandidateInfo{}, invalid(jsonCandidate)
	}
	if q.DraftID != "" {
		if !digest(q.DraftID, 32) {
			return CandidateInfo{}, invalid(jsonDraftId)
		}
		d, err := s.cfg.Candidates.Draft(ctx, q.DraftID)
		return draftInfo(d), publicError(err, OperationFailed, "")
	}
	if !digest(q.Candidate, 64) {
		return CandidateInfo{}, invalid(jsonCandidate)
	}
	f, err := s.cfg.Candidates.Load(ctx, q.Candidate)
	if err != nil {
		return CandidateInfo{}, publicError(err, OperationFailed, "")
	}
	return CandidateInfo{Candidate: f.Digest(), Components: f.Manifest().Components}, nil
}

// ReadCandidate returns bounded exact bytes from one identified snapshot.
func (s *Service) ReadCandidate(ctx context.Context, q ReadCandidateRequest) (CandidateRead, error) {
	if err := s.authorizeRequest(ctx, ReadOperation, q); err != nil {
		return CandidateRead{}, err
	}
	if q.Offset < 0 || q.MaxBytes < 0 {
		return CandidateRead{}, invalid(jsonOffset)
	}
	if (q.DraftID == "") == (q.Candidate == "") || q.Component == "" {
		return CandidateRead{}, invalid(jsonCandidate)
	}
	components, err := s.readComponents(ctx, q)
	if err != nil {
		return CandidateRead{}, err
	}
	for _, c := range components {
		if c.Name != q.Component {
			continue
		}
		total := int64(len(c.Data))
		if q.Offset > total {
			return CandidateRead{}, invalid(jsonOffset)
		}
		limit := bounded(q.MaxBytes, s.cfg.Limits.MaxReadBytes)
		end := q.Offset + min(int64(limit), total-q.Offset)
		hash := sha256.Sum256(c.Data)
		out := CandidateRead{Data: slices.Clone(c.Data[q.Offset:end]), MediaType: c.MediaType, Digest: hex.EncodeToString(hash[:]), Offset: q.Offset, Total: total, Truncated: end < total}
		if out.Truncated {
			out.NextOffset = &end
		}
		return out, nil
	}
	return CandidateRead{}, NewError(NotFound, "", "", "component")
}

// Edit atomically validates one component mutation at the expected revision.
func (s *Service) Edit(ctx context.Context, q EditRequest) (CandidateInfo, error) {
	if err := s.authorizeRequest(ctx, EditOperation, q); err != nil {
		return CandidateInfo{}, err
	}
	if !digest(q.DraftID, 32) || q.ExpectedRevision == 0 {
		return CandidateInfo{}, invalid(jsonExpectedRevision)
	}
	edit := candidate.Edit{Operation: q.Operation, Name: q.Name}
	switch q.Operation {
	case candidate.Put:
		component, err := s.putComponent(q)
		if err != nil {
			return CandidateInfo{}, err
		}
		edit.Component = component

	case candidate.Delete:
		if q.Content != nil || q.ContentBase64 != nil || q.Slot != "" || q.MediaType != "" || len(q.Dependencies) != 0 {
			return CandidateInfo{}, invalid(jsonContent)
		}
	default:
		return CandidateInfo{}, invalid(jsonOperation)
	}
	d, err := s.cfg.Candidates.Edit(ctx, q.DraftID, q.ExpectedRevision, []candidate.Edit{edit})
	return draftInfo(d), publicError(err, InvalidInput, jsonContent)
}

// Validate verifies the current draft using the core and host validators.
func (s *Service) Validate(ctx context.Context, q RevisionRequest) (Validation, error) {
	if err := s.authorizeRequest(ctx, ValidateOperation, q); err != nil {
		return Validation{}, err
	}
	if !digest(q.DraftID, 32) || q.ExpectedRevision == 0 {
		return Validation{}, invalid(jsonExpectedRevision)
	}
	err := s.cfg.Candidates.Validate(ctx, q.DraftID, q.ExpectedRevision)
	return Validation{Valid: err == nil}, publicError(err, InvalidInput, jsonContent)
}

// Freeze retains one validated immutable revision for submission.
func (s *Service) Freeze(ctx context.Context, q RevisionRequest) (Frozen, error) {
	if err := s.authorizeRequest(ctx, FreezeOperation, q); err != nil {
		return Frozen{}, err
	}
	if !digest(q.DraftID, 32) || q.ExpectedRevision == 0 {
		return Frozen{}, invalid(jsonExpectedRevision)
	}
	f, err := s.cfg.Candidates.Freeze(ctx, q.DraftID, q.ExpectedRevision)
	if err != nil {
		return Frozen{}, publicError(err, InvalidInput, jsonContent)
	}
	return Frozen{Candidate: f.Digest(), Manifest: f.Manifest()}, nil
}

func draftInfo(d candidate.Draft) CandidateInfo {
	out := CandidateInfo{DraftID: d.ID, Revision: d.Revision, Candidate: d.FrozenDigest, Components: make([]candidate.ComponentRef, 0, len(d.Components))}
	for _, c := range d.Components {
		hash := sha256.Sum256(c.Data)
		out.Components = append(out.Components, candidate.ComponentRef{Name: c.Name, Slot: c.Slot, MediaType: c.MediaType, Digest: hex.EncodeToString(hash[:]), Size: int64(len(c.Data)), Dependencies: slices.Clone(c.Dependencies)})
	}
	return out
}
func bounded(n, cap int) int {
	if n == 0 || n > cap {
		return cap
	}
	return n
}

// readComponents selects exactly one immutable frozen or optimistic draft view.
func (s *Service) readComponents(ctx context.Context, q ReadCandidateRequest) ([]candidate.Component, error) {
	if q.DraftID != "" {
		if !digest(q.DraftID, 32) || q.ExpectedRevision == 0 {
			return nil, invalid(jsonExpectedRevision)
		}
		d, err := s.cfg.Candidates.Draft(ctx, q.DraftID)
		if err != nil {
			return nil, publicError(err, OperationFailed, "")
		}
		if d.Revision != q.ExpectedRevision {
			return nil, NewError(RevisionConflict, "", "", jsonExpectedRevision)
		}
		return d.Components, nil
	} else {
		if !digest(q.Candidate, 64) || q.ExpectedRevision != 0 {
			return nil, invalid(jsonCandidate)
		}
		f, err := s.cfg.Candidates.Load(ctx, q.Candidate)
		if err != nil {
			return nil, publicError(err, OperationFailed, "")
		}
		return f.Components(), nil
	}
}
