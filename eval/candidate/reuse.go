package candidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type copyPlanIdentity struct {
	Domain            string         `json:"domain"`
	Version           int            `json:"version"`
	DraftID           string         `json:"draft_id"`
	Revision          uint64         `json:"revision"`
	DestinationDigest string         `json:"destination_digest"`
	SurfaceDigest     string         `json:"surface_digest"`
	BaselineDigest    string         `json:"baseline_digest"`
	SourceCandidate   string         `json:"source_candidate"`
	Selected          []string       `json:"selected"`
	Closure           []ComponentRef `json:"closure"`
	Replace           []string       `json:"replace"`
}

type copyProposal struct {
	edits   []Edit
	origins []ComponentOrigin
	refs    []ComponentRef
	replace []string
}

// PreviewCopy returns the full proposed dependency closure without changing the draft.
// It does not reserve or persist a plan. The host must authorize the operation.
func (s *Store) PreviewCopy(ctx context.Context, id string, expected uint64, request PreviewCopyRequest) (CopyPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, _, err := s.planCopy(ctx, id, expected, request)
	return plan, err
}

// Copy applies exactly one confirmed preview in one atomic draft revision.
// PlanDigest confirms the whole closure; Replace confirms every name collision.
// The host must recheck its operation grants before calling this method.
func (s *Store) Copy(ctx context.Context, id string, expected uint64, request CopyRequest) (CopyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, draft, err := s.planCopy(ctx, id, expected, PreviewCopyRequest{SourceCandidate: request.SourceCandidate, Components: request.Components})
	if err != nil {
		return CopyResult{}, err
	}
	replacements, err := normalizedNames(request.Replace, false)
	if err != nil {
		return CopyResult{}, err
	}
	if !validHex(request.PlanDigest, 64) || request.PlanDigest != plan.Digest || !slices.Equal(replacements, plan.Replace) {
		return CopyResult{}, ErrCopyPlan
	}
	if err := ctx.Err(); err != nil {
		return CopyResult{}, err
	}
	draft.Revision++
	if err := s.write("draft-"+id+".json", draft); err != nil {
		return CopyResult{}, err
	}
	names := make([]string, 0, len(plan.Closure))
	for _, ref := range plan.Closure {
		names = append(names, ref.Name)
	}
	return CopyResult{Draft: draft, PlanDigest: plan.Digest, Selected: plan.Selected, Closure: names}, nil
}

// normalizedNames validates and owns caller selections without silently deduplicating.
func normalizedNames(names []string, required bool) ([]string, error) {
	if required && len(names) == 0 {
		return nil, errors.New("candidate: empty component selection")
	}
	result := append([]string{}, names...)
	slices.Sort(result)
	for i, name := range result {
		if !validName(name) || (i > 0 && result[i-1] == name) {
			return nil, fmt.Errorf("candidate: invalid or duplicate component %q", name)
		}
	}
	return result, nil
}

// copyPlanDigest binds the exact destination, selected source closure and collisions.
func copyPlanDigest(plan CopyPlan) (string, error) {
	identity := copyPlanIdentity{Domain: "loom.candidate.copy", Version: plan.Version, DraftID: plan.DraftID, Revision: plan.Revision, DestinationDigest: plan.DestinationDigest, SurfaceDigest: plan.SurfaceDigest, BaselineDigest: plan.BaselineDigest, SourceCandidate: plan.SourceCandidate, Selected: plan.Selected, Closure: plan.Closure, Replace: plan.Replace}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

// planCopy builds a complete validated proposal under the store lock, without writes.
func (s *Store) planCopy(ctx context.Context, id string, expected uint64, request PreviewCopyRequest) (CopyPlan, Draft, error) {
	d, err := s.current(ctx, id, expected)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	if d.FrozenDigest != "" {
		return CopyPlan{}, Draft{}, ErrFrozen
	}
	if d.Revision == ^uint64(0) {
		return CopyPlan{}, Draft{}, errors.New("candidate: revision exhausted")
	}
	selected, err := normalizedNames(request.Components, true)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	source, err := s.load(request.SourceCandidate)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	names, err := source.Closure(selected)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	plan, err := s.initialCopyPlan(d, source, selected)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	proposal := proposeCopy(d, source, names)
	plan.Closure = proposal.refs
	plan.Replace = proposal.replace
	proposed, err := s.apply(d.Components, proposal.edits)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	if err := s.validate(ctx, proposed); err != nil {
		return CopyPlan{}, Draft{}, err
	}

	plan.Digest, err = copyPlanDigest(plan)
	if err != nil {
		return CopyPlan{}, Draft{}, err
	}
	d.Components = proposed
	d.Origins = proposal.origins
	return plan, d, nil
}

// proposeCopy selects owned source bytes and replaces only the selected origins.
func proposeCopy(d Draft, source *Frozen, names []string) copyProposal {
	refs := make([]ComponentRef, 0, len(names))
	replace := make([]string, 0, len(names))
	existing := make(map[string]bool, len(d.Components))
	for _, c := range d.Components {
		existing[c.Name] = true
	}
	imported := make(map[string]bool, len(names))
	edits := make([]Edit, 0, len(names))
	origins := make([]ComponentOrigin, 0, len(d.Origins)+len(names))
	for _, name := range names {
		imported[name] = true
	}
	for _, origin := range d.Origins {
		if !imported[origin.Name] {
			origins = append(origins, origin)
		}
	}
	for _, c := range source.components {
		if !imported[c.Name] {
			continue
		}
		ref := componentRef(c)
		refs = append(refs, ref)
		if existing[c.Name] {
			replace = append(replace, c.Name)
		}
		edits = append(edits, Edit{Operation: Put, Component: c})
		origins = append(origins, ComponentOrigin{Name: c.Name, SourceCandidate: source.Digest(), SourceDigest: ref.Digest})
	}

	slices.SortFunc(origins, func(a, b ComponentOrigin) int {
		return strings.Compare(a.Name, b.Name)
	})
	return copyProposal{edits: edits, origins: origins, refs: refs, replace: replace}
}

// initialCopyPlan binds current destination content without freezing or writing it.
func (s *Store) initialCopyPlan(d Draft, source *Frozen, selected []string) (CopyPlan, error) {
	destination, err := makeFrozenWithOrigins(s.surface, s.baseline.Digest(), d.Components, d.Origins)
	if err != nil {
		return CopyPlan{}, err
	}
	return CopyPlan{Version: 1, DraftID: d.ID, Revision: d.Revision, DestinationDigest: destination.Digest(), SurfaceDigest: destination.manifest.SurfaceDigest, BaselineDigest: s.baseline.Digest(), SourceCandidate: source.Digest(), Selected: selected}, nil
}
