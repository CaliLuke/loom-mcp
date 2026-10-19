package inspect

import (
	"context"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"reflect"
	"sort"
)

func componentChanges(baseline, frozen candidate.Manifest) []semanticChange {
	var changes []semanticChange
	before := map[string]candidate.ComponentRef{}
	after := map[string]candidate.ComponentRef{}
	names := map[string]bool{}
	for _, c := range baseline.Components {
		before[c.Name] = c
		names[c.Name] = true
	}
	for _, c := range frozen.Components {
		after[c.Name] = c
		names[c.Name] = true
	}
	for name := range names {
		a, aok := before[name]
		z, zok := after[name]
		if aok && zok && reflect.DeepEqual(a, z) {
			continue
		}
		c := semanticChange{Name: name, Operation: "modify"}
		if !aok {
			c.Operation = "add"
		}
		if !zok {
			c.Operation = "delete"
		}
		if aok {
			c.BeforeDigest = a.Digest
			c.BeforeKey = entryKey(BaselineComponent, name)
		}
		if zok {
			c.AfterDigest = z.Digest
			c.AfterKey = entryKey(CandidateComponent, name)
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

func (b *publicationBuilder) sources(ctx context.Context) error {
	if b.input.BaselineDigest == "" {
		b.input.BaselineDigest = b.workspace.candidates.Discover().BaselineDigest
	}
	if !validDigest(b.input.BaselineDigest) || !validDigest(b.input.CandidateDigest) {
		return ErrInvalidInput
	}
	baseline, err := b.workspace.candidates.Load(ctx, b.input.BaselineDigest)
	if err != nil {
		return ErrNotFound
	}
	frozen, err := b.workspace.candidates.Load(ctx, b.input.CandidateDigest)
	if err != nil {
		return ErrNotFound
	}
	if frozen.Manifest().SurfaceDigest != baseline.Manifest().SurfaceDigest {
		return ErrInvalidInput
	}
	if frozen.Digest() != baseline.Digest() && frozen.Manifest().BaselineDigest != baseline.Digest() {
		return ErrInvalidInput
	}
	if b.attempt != nil && b.attempt.Prepared != nil && b.attempt.Prepared.CandidateDigest != frozen.Digest() {
		return ErrInvalidInput
	}
	for _, snapshot := range []struct {
		kind Kind
		f    *candidate.Frozen
	}{{BaselineComponent, baseline}, {CandidateComponent, frozen}} {
		for _, c := range snapshot.f.Components() {
			if err := b.add(snapshot.kind, c.Name, c.MediaType, c.Data, bodyProvider{Kind: "candidate", Candidate: snapshot.f.Digest(), Name: c.Name}, []Role{Editor}, false); err != nil {
				return err
			}
		}
	}
	b.changes = componentChanges(baseline.Manifest(), frozen.Manifest())
	return nil
}
