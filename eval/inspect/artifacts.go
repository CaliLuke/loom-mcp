package inspect

import (
	"context"
	"mime"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type sourceEntry struct {
	key       string
	entry     Entry
	provider  bodyProvider
	data      []byte
	audiences []Role
	sourceKey string
}
type publicationBuilder struct {
	workspace *Workspace
	input     Publication
	entries   []sourceEntry
	changes   []semanticChange
	blobs     map[string][]byte
	attempt   *eval.StoredAttempt
}

// Publish retains role-approved evidence as one immutable, content-addressed manifest.
func (w *Workspace) Publish(ctx context.Context, input Publication) (ManifestRef, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !validOrigin(input.Origin) || len(input.Policies) == 0 {
		return "", ErrInvalidInput
	}
	exp, err := w.experiments.Load(input.Origin.ExperimentID)
	if err != nil {
		return "", ErrNotFound
	}
	if !planned(exp, input.Origin.Key) {
		return "", ErrNotFound
	}
	b := publicationBuilder{workspace: w, input: input, blobs: map[string][]byte{}}
	for _, a := range exp.Attempts {
		if a.AttemptKey == input.Origin.Key {
			b.attempt = a
			break
		}
	}
	if err := b.collect(ctx); err != nil {
		return "", err
	}
	m, err := b.project()
	if err != nil {
		return "", err
	}
	if len(m.Entries) > w.limits.MaxEntries {
		return "", ErrInvalidInput
	}
	ref, derived, diffs, err := materialize(m)
	if err != nil {
		return "", err
	}
	disk := diskManifest{m, derived}
	if validateManifest(disk, ref) != nil {
		return "", ErrIntegrity
	}
	return w.persistPublication(ref, disk, b.blobs, diffs)
}

func planned(e *eval.Experiment, key eval.AttemptKey) bool {
	variant := false
	for _, v := range e.Header.Variants {
		if v.ID == key.Variant {
			variant = true
		}
	}
	if !variant || key.Repeat > max(1, e.Header.Config.Repeats) {
		return false
	}
	for _, s := range e.Header.Scenarios {
		if s.ID == key.Scenario && slices.Contains(s.Items, key.Item) {
			return true
		}
	}
	return false
}

func allowed(k Kind, r Role) bool {
	switch k {
	case TaskSource, Rendered:
		return validRole(r)
	case Deliverable:
		return r == Editor || r == Selector || r == Evaluator
	case BaselineComponent, CandidateComponent, ComponentDiff, DecisionNote:
		return r == Editor
	case RunView, CallView:
		return r == Editor || r == Evaluator
	case EvaluatorReference:
		return r == Evaluator
	default:
		return false
	}
}

func entryKey(k Kind, name string) string { return string(k) + ":" + name }

func validMedia(value string) bool { _, _, err := mime.ParseMediaType(value); return err == nil }

func (w *Workspace) persistPublication(ref ManifestRef, disk diskManifest, blobs, diffs map[string][]byte) (ManifestRef, error) {
	for d, data := range diffs {
		blobs[d] = data
	}
	for d, data := range blobs {
		if int64(len(data)) > w.limits.MaxArtifactBytes {
			return "", ErrInvalidInput
		}
		if err := w.publishFile("blobs", d, data); err != nil {
			return "", err
		}
	}
	data, err := encode(disk)
	if err != nil {
		return "", ErrIntegrity
	}
	if err := w.publishFile("manifests", string(ref)+".json", data); err != nil {
		return "", err
	}
	return ref, nil
}

func (b *publicationBuilder) add(k Kind, name, media string, data []byte, provider bodyProvider, audiences []Role, provisional bool) error {
	if !validName(name) || !validMedia(media) || int64(len(data)) > b.workspace.limits.MaxArtifactBytes {
		return ErrInvalidInput
	}
	key := entryKey(k, name)
	for _, e := range b.entries {
		if e.key == key {
			return ErrInvalidInput
		}
	}
	state := Available
	if provisional {
		state = Provisional
	}
	n := int64(len(data))
	d := digest(data)
	provider.Digest = d
	provider.Size = n
	p := &Provenance{ExperimentID: b.input.Origin.ExperimentID, Key: &b.input.Origin.Key, CandidateDigest: b.input.CandidateDigest, BaselineDigest: b.input.BaselineDigest}
	e := Entry{Kind: k, Label: name, MediaType: media, State: state, Size: &n, Digest: d, SourceDigest: d, Provenance: p}
	b.entries = append(b.entries, sourceEntry{key, e, provider, slices.Clone(data), slices.Clone(audiences), ""})
	return nil
}

func (b *publicationBuilder) outputs(ctx context.Context) error {
	present := map[string]eval.StoredArtifact{}
	if b.attempt != nil {
		for _, a := range b.attempt.Artifacts {
			if a.Size > b.workspace.limits.MaxArtifactBytes {
				return ErrInvalidInput
			}
			present[a.Name] = a
			data, err := b.workspace.experiments.ReadArtifact(ctx, b.input.Origin.ExperimentID, b.input.Origin.Key, a.Name)
			if err != nil {
				b.missing(Deliverable, a.Name, "missing_content", []Role{Editor, Selector, Evaluator})
				continue
			}
			if int64(len(data)) != a.Size || digest(data) != a.Digest {
				return ErrIntegrity
			}
			if err := b.add(Deliverable, a.Name, a.MediaType, data, bodyProvider{Kind: "artifact", Name: a.Name}, []Role{Editor, Selector, Evaluator}, a.Provisional || b.input.Provisional); err != nil {
				return err
			}
		}
	}
	return b.declaredOutputs(present)
}

// declaredOutputs retains names only when explicitly supplied by the host.
func (b *publicationBuilder) declaredOutputs(present map[string]eval.StoredArtifact) error {
	seen := map[string]bool{}
	for _, d := range b.input.ExpectedDeliverables {
		if !validName(d.Name) || !validMedia(d.MediaType) || seen[d.Name] {
			return ErrInvalidInput
		}
		seen[d.Name] = true
		if a, ok := present[d.Name]; ok {
			if a.MediaType != d.MediaType {
				return ErrInvalidInput
			}
		} else {
			b.missing(Deliverable, d.Name, "missing_content", []Role{Editor, Selector, Evaluator})
			b.entries[len(b.entries)-1].entry.MediaType = d.MediaType
		}
	}
	if len(seen) == 0 && b.attempt != nil && b.attempt.Probe != nil && b.attempt.Probe.Delivery == "incomplete" {
		b.missing(Deliverable, "delivery incomplete", "delivery_incomplete", []Role{Editor, Selector, Evaluator})
		// Aggregate diagnostics cannot alias a caller-owned deliverable name.
		b.entries[len(b.entries)-1].key = "diagnostic:delivery_incomplete"
	}
	return nil
}

func (b *publicationBuilder) missing(k Kind, name, code string, audiences []Role) {
	b.entries = append(b.entries, sourceEntry{key: entryKey(k, name), entry: Entry{Kind: k, Label: name, State: Missing, Code: code, Incomplete: true}, audiences: audiences})
}

func (b *publicationBuilder) attachments() error {
	for _, a := range b.input.Attachments {
		if a.Kind != TaskSource && a.Kind != DecisionNote && a.Kind != EvaluatorReference || len(a.Audiences) == 0 {
			return ErrInvalidInput
		}
		seen := map[Role]bool{}
		for _, r := range a.Audiences {
			if !allowed(a.Kind, r) || seen[r] {
				return ErrInvalidInput
			}
			seen[r] = true
		}
		if len(a.Provenance) > 4096 || strings.ContainsRune(a.Provenance, 0) {
			return ErrInvalidInput
		}
		if err := b.add(a.Kind, a.Name, a.MediaType, a.Data, bodyProvider{Kind: blobProvider}, a.Audiences, b.input.Provisional); err != nil {
			return err
		}
		b.entries[len(b.entries)-1].entry.Provenance.Note = a.Provenance
	}
	return nil
}

func (b *publicationBuilder) renders() error {
	for _, r := range b.input.Renderings {
		if r.SourceKind != TaskSource && r.SourceKind != Deliverable && r.SourceKind != BaselineComponent && r.SourceKind != CandidateComponent || r.RendererVersion == "" || len(r.Audiences) == 0 {
			return ErrInvalidInput
		}
		var source *sourceEntry
		for i := range b.entries {
			if b.entries[i].key == entryKey(r.SourceKind, r.SourceName) {
				source = &b.entries[i]
				break
			}
		}
		if source == nil || source.entry.State == Missing || source.entry.SourceDigest != r.SourceDigest {
			return ErrInvalidInput
		}
		seen := map[Role]bool{}
		for _, role := range r.Audiences {
			if !slices.Contains(source.audiences, role) || seen[role] {
				return ErrInvalidInput
			}
			seen[role] = true
		}
		sourceKey := source.key
		state := source.entry.State
		if err := b.add(Rendered, r.Name, r.MediaType, r.Data, bodyProvider{Kind: blobProvider}, r.Audiences, state == Provisional); err != nil {
			return err
		}
		last := &b.entries[len(b.entries)-1]
		last.sourceKey = sourceKey
		last.entry.SourceDigest = r.SourceDigest
		last.entry.RendererVersion = r.RendererVersion
	}
	return nil
}

// collect snapshots owners before materializing any model-facing role projection.
func (b *publicationBuilder) collect(ctx context.Context) error {
	steps := []func() error{func() error { return b.sources(ctx) }, func() error { return b.outputs(ctx) }, b.attachments, b.renders, func() error { return b.canonical(ctx) }}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
