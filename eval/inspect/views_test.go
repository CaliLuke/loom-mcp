package inspect

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	w           *Workspace
	pub         Publication
	root        string
	experiments *eval.Store
	candidates  *candidate.Store
}

func TestViewsEnforceRoleScopeAndOpaqueSelector(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	editor, ed := entriesFor(t, f, ref, Editor, "editor/v1")
	selector, se := entriesFor(t, f, ref, Selector, "selector/v1")
	assert.NotEmpty(t, entryOf(t, ed, ComponentDiff).Locator)
	for _, e := range se {
		assert.Contains(t, []Kind{TaskSource, Deliverable, Rendered}, e.Kind)
		assert.Nil(t, e.Provenance)
		assert.Empty(t, e.SourceDigest)
		assert.NotContains(t, e.Label, "unopened")
		assert.NotContains(t, e.Locator, f.pub.Origin.ExperimentID)
	}
	_, err = selector.Read(t.Context(), ReadQuery{Locator: entryOf(t, ed, DecisionNote).Locator})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = editor.Read(t.Context(), ReadQuery{Locator: "../secret"})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{Editor, "other-policy"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReadsPageExactBytesAndExplicitMissing(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, entries := entriesFor(t, f, ref, Editor, "editor/v1")
	source := entryOf(t, entries, TaskSource)
	got, err := v.Read(t.Context(), ReadQuery{Locator: source.Locator, MaxBytes: 2})
	require.NoError(t, err)
	assert.Equal(t, []byte("ab"), got.Data)
	require.NotNil(t, got.NextOffset)
	assert.EqualValues(t, 2, *got.NextOffset)
	assert.True(t, got.Truncated)
	assert.EqualValues(t, 6, *got.Total)
	got, err = v.Read(t.Context(), ReadQuery{Locator: source.Locator, Offset: 6})
	require.NoError(t, err)
	assert.Empty(t, got.Data)
	assert.False(t, got.Truncated)
	_, err = v.Read(t.Context(), ReadQuery{Locator: source.Locator, Offset: 7})
	require.ErrorIs(t, err, ErrInvalidInput)
	missing := entryOf(t, entries, Deliverable)
	assert.Equal(t, Missing, missing.State)
	assert.Empty(t, missing.Digest)
	assert.Nil(t, missing.Size)
	page, err := v.List(t.Context(), ListQuery{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, page.Entries, 1)
	assert.True(t, page.Truncated)
}

func TestPublicationOwnsBytesAndRestartMaterialization(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, entries := entriesFor(t, f, ref, Editor, "editor/v1")
	e := entryOf(t, entries, TaskSource)
	f.pub.Attachments[0].Data[0] = 'Z'
	got, err := v.Read(t.Context(), ReadQuery{Locator: e.Locator})
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(got.Data))
	got.Data[0] = 'X'
	reopened, err := Open(f.root, f.experiments, f.candidates, DefaultLimits())
	require.NoError(t, err)
	f.w = reopened
	other, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	assert.Equal(t, entries, rows)
	again, err := other.Read(t.Context(), ReadQuery{Locator: e.Locator})
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(again.Data))
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	experiments, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	surface := candidate.Surface{ID: "test", Version: "1", MaxTotalBytes: 4096, Slots: []candidate.Slot{{Name: "source", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/plain"}, MaxBytes: 4096, MaxComponents: 2, Binding: candidate.Binding{ID: "files", Version: "1"}}}}
	candidates, err := candidate.Open(t.Context(), t.TempDir(), surface, []candidate.Component{{Name: "main.txt", Slot: "source", MediaType: "text/plain", Data: []byte("before")}})
	require.NoError(t, err)
	draft, err := candidates.Create(t.Context())
	require.NoError(t, err)
	draft, err = candidates.Edit(t.Context(), draft.ID, draft.Revision, []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "main.txt", Slot: "source", MediaType: "text/plain", Data: []byte("after")}}})
	require.NoError(t, err)
	frozen, err := candidates.Freeze(t.Context(), draft.ID, draft.Revision)
	require.NoError(t, err)
	key := eval.AttemptKey{Scenario: "task", Item: "item", Variant: "private-proposal", Repeat: 1}
	cfg := eval.ProbeConfig{Key: key, Item: eval.Item{ID: key.Item, Input: "input"}, Variant: eval.Variant{ID: key.Variant}, Agent: "solver", Timeout: time.Second, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return nil, errors.New("unused")
	}}
	writer, err := experiments.BeginProbe(t.Context(), cfg, eval.ExperimentMeta{})
	require.NoError(t, err)
	root := filepath.Join(t.TempDir(), "workspace")
	w, err := Open(root, experiments, candidates, DefaultLimits())
	require.NoError(t, err)
	pub := Publication{Origin: Origin{writer.ID(), key}, CandidateDigest: frozen.Digest(), ExpectedDeliverables: []eval.Deliverable{{Name: "missing.txt", MediaType: "text/plain"}}, Policies: []Policy{{Role: Editor, Name: "editor/v1"}, {Role: Selector, Name: "selector/v1"}, {Role: Evaluator, Name: "evaluator/v1"}}, Attachments: []Attachment{{Name: "unopened.txt", Kind: TaskSource, MediaType: "text/plain", Data: []byte("abcdef"), Audiences: []Role{Editor, Selector}}, {Name: "private-note.txt", Kind: DecisionNote, MediaType: "text/plain", Data: []byte("decision"), Audiences: []Role{Editor}}, {Name: "secret.txt", Kind: EvaluatorReference, MediaType: "text/plain", Data: []byte("answer key"), Audiences: []Role{Evaluator}}}}
	return fixture{w, pub, root, experiments, candidates}
}

func entriesFor(t *testing.T, f fixture, ref ManifestRef, role Role, name string) (*View, []Entry) {
	t.Helper()
	v, err := f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{role, name})
	require.NoError(t, err)
	page, err := v.List(t.Context(), ListQuery{})
	require.NoError(t, err)
	return v, page.Entries
}

func entryOf(t *testing.T, entries []Entry, kind Kind) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("missing kind %s", kind)
	return Entry{}
}
