package inspect

import (
	"slices"
	"sort"
)

// linkEntries limits every derived reference to its already projected role.
func linkEntries(entries []entrySpec) {
	// Every derived link must remain within the same projected role.
	for i := range entries {
		s := &entries[i]
		available := map[string]bool{}
		for _, other := range entries {
			if other.Role == s.Role && other.Entry.State != Withheld {
				available[other.Key] = true
			}
		}
		if s.SourceKey != "" && !available[s.SourceKey] {
			s.Entry = Entry{Kind: s.Entry.Kind, Label: "withheld", State: Withheld}
			s.Provider = bodyProvider{}
			s.SourceKey = ""
		}
		if s.Entry.Kind == ComponentDiff {
			for j := range s.Changes {
				if !available[s.Changes[j].BeforeKey] {
					s.Changes[j].BeforeKey = ""
				}
				if !available[s.Changes[j].AfterKey] {
					s.Changes[j].AfterKey = ""
				}
			}
			for _, other := range entries {
				if other.Role == s.Role && (other.Entry.Kind == Deliverable || other.Entry.Kind == RunView) && other.Entry.State != Withheld {
					s.LinkKeys = append(s.LinkKeys, other.Key)
				}
			}
			sort.Strings(s.LinkKeys)
		}
	}
}

func (b *publicationBuilder) project() (semanticManifest, error) {
	policies := slices.Clone(b.input.Policies)
	sort.Slice(policies, func(i, j int) bool { return policies[i].Role < policies[j].Role })
	m := b.manifestBase()
	seen := map[Role]bool{}
	for _, p := range policies {
		if !validRole(p.Role) || p.Name == "" || len(p.Name) > 256 || seen[p.Role] {
			return m, ErrInvalidInput
		}
		seen[p.Role] = true
		m.Policies = append(m.Policies, ViewPolicy{p.Role, p.Name})
		for _, raw := range b.entries {
			if !allowed(raw.entry.Kind, p.Role) || !slices.Contains(raw.audiences, p.Role) {
				continue
			}
			// Canonical bodies are already independently projected for this exact policy.
			if (raw.entry.Kind == RunView || raw.entry.Kind == CallView) && raw.entry.State != Missing {
				if raw.provider.Name != string(p.Role) {
					continue
				}
			}
			s, err := b.projectEntry(raw, p)
			if err != nil {
				return m, err
			}
			m.Entries = append(m.Entries, s)
		}

		if p.Role == Editor {
			m.Entries = append(m.Entries, entrySpec{Key: "diff", Role: p.Role, Policy: p.Name, Entry: Entry{Kind: ComponentDiff, Label: "component changes", State: Available, RendererVersion: "loom.inspect.component-diff.v1", Provenance: &Provenance{ExperimentID: b.input.Origin.ExperimentID, Key: &b.input.Origin.Key, CandidateDigest: b.input.CandidateDigest, BaselineDigest: b.input.BaselineDigest}}, Changes: slices.Clone(b.changes)})
		}
	}
	linkEntries(m.Entries)
	sort.Slice(m.Entries, func(i, j int) bool { return specLess(m.Entries[i], m.Entries[j]) })
	return m, nil
}

func (b *publicationBuilder) projectEntry(raw sourceEntry, p Policy) (entrySpec, error) {
	s := entrySpec{Key: raw.key, Role: p.Role, Policy: p.Name, Entry: raw.entry, Provider: raw.provider, SourceKey: raw.sourceKey}
	if s.Entry.State != Missing {
		data := slices.Clone(raw.data)
		if p.ArtifactContent != nil && raw.entry.Kind != RunView && raw.entry.Kind != CallView {
			value, include, err := p.ArtifactContent(ArtifactContent{p.Role, raw.entry.Kind, raw.entry.Label, raw.entry.MediaType, raw.entry.SourceDigest, data})
			if err != nil {
				return entrySpec{}, ErrInvalidInput
			}
			if !include {
				s.Entry = Entry{Kind: raw.entry.Kind, Label: "withheld", State: Withheld}
				s.Provider = bodyProvider{}
				s.SourceKey = ""
				return s, nil
			}
			data = slices.Clone(value)
		}
		if int64(len(data)) > b.workspace.limits.MaxArtifactBytes {
			return entrySpec{}, ErrInvalidInput
		}
		d := digest(data)
		n := int64(len(data))
		s.Entry.Digest = d
		s.Entry.Size = &n
		if d != raw.provider.Digest || raw.provider.Kind == blobProvider {
			s.Provider = bodyProvider{Kind: blobProvider, Digest: d, Size: n}
			b.blobs[d] = data
		}
	}
	return s, nil
}

func (b *publicationBuilder) manifestBase() semanticManifest {
	m := semanticManifest{Version: 1, Origin: b.input.Origin, CandidateDigest: b.input.CandidateDigest, BaselineDigest: b.input.BaselineDigest, ExpectedDeliverables: slices.Clone(b.input.ExpectedDeliverables), Provisional: b.input.Provisional}
	if b.attempt != nil {
		m.Receipt = b.attempt.Prepared
	}
	sort.Slice(m.ExpectedDeliverables, func(i, j int) bool { return m.ExpectedDeliverables[i].Name < m.ExpectedDeliverables[j].Name })

	return m
}
