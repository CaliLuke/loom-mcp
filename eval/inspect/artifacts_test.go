package inspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingCaptureRemainsVisible(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	_, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	e := entryOf(t, rows, RunView)
	assert.Equal(t, Missing, e.State)
	assert.Equal(t, "canonical_evidence_missing", e.Code)
}

func TestArtifactRedactionIsImmutableAndWithheld(t *testing.T) {
	f := newFixture(t)
	f.pub.Policies[0].ArtifactContent = func(c ArtifactContent) ([]byte, bool, error) {
		if c.Kind == DecisionNote {
			return nil, false, nil
		}
		if c.Kind == TaskSource {
			return []byte("approved"), true, nil
		}
		return c.Data, true, nil
	}
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	note := entryOf(t, rows, DecisionNote)
	assert.Equal(t, Withheld, note.State)
	assert.Empty(t, note.SourceDigest)
	assert.Nil(t, note.Provenance)
	result, err := v.Read(t.Context(), ReadQuery{Locator: note.Locator})
	require.NoError(t, err)
	assert.Nil(t, result.Total)
	assert.Empty(t, result.Data)
	source := entryOf(t, rows, TaskSource)
	assert.NotEqual(t, source.SourceDigest, source.Digest)
	result, err = v.Read(t.Context(), ReadQuery{Locator: source.Locator})
	require.NoError(t, err)
	assert.Equal(t, "approved", string(result.Data))
	again, err := Open(f.root, f.experiments, f.candidates, DefaultLimits())
	require.NoError(t, err)
	f.w = again
	v, _ = entriesFor(t, f, ref, Editor, "editor/v1")
	result, err = v.Read(t.Context(), ReadQuery{Locator: source.Locator})
	require.NoError(t, err)
	assert.Equal(t, "approved", string(result.Data))
}

func TestEmptyAndProvisionalAttachments(t *testing.T) {
	f := newFixture(t)
	f.pub.Provisional = true
	f.pub.Attachments[0].Data = []byte{}
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	e := entryOf(t, rows, TaskSource)
	assert.Equal(t, Provisional, e.State)
	require.NotNil(t, e.Size)
	assert.Zero(t, *e.Size)
	assert.Equal(t, digest(nil), e.Digest)
	result, err := v.Read(t.Context(), ReadQuery{Locator: e.Locator})
	require.NoError(t, err)
	require.NotNil(t, result.Total)
	assert.Zero(t, *result.Total)
	assert.False(t, result.Truncated)
}

func TestPublicationValidatesHostInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Publication)
	}{
		{"reference_to_editor", func(p *Publication) { p.Attachments[2].Audiences = []Role{Editor} }},
		{"escaping_name", func(p *Publication) { p.Attachments[0].Name = "../secret" }},
		{"duplicate_name", func(p *Publication) { p.Attachments = append(p.Attachments, p.Attachments[0]) }},
		{"duplicate_policy", func(p *Publication) { p.Policies = append(p.Policies, p.Policies[0]) }},
		{"render_source_mismatch", func(p *Publication) {
			p.Renderings = []Rendering{{Name: "render", SourceKind: TaskSource, SourceName: "unopened.txt", SourceDigest: digest([]byte("other")), RendererVersion: "v1", MediaType: "text/plain", Audiences: []Role{Editor}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(&f.pub)
			_, err := f.w.Publish(t.Context(), f.pub)
			assert.ErrorIs(t, err, ErrInvalidInput)
		})
	}
}

func TestManifestTamperingFailsClosed(t *testing.T) {
	for _, field := range []string{"locator", "link", "diff_digest", "semantic_reference", "unknown_field"} {
		t.Run(field, func(t *testing.T) {
			f := newFixture(t)
			ref, err := f.w.Publish(t.Context(), f.pub)
			require.NoError(t, err)
			path := filepath.Join(f.root, "manifests", string(ref)+".json")
			root, err := os.OpenRoot(f.root)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, root.Close()) })
			data, err := root.ReadFile(filepath.Join("manifests", string(ref)+".json"))
			require.NoError(t, err)
			var m diskManifest
			require.NoError(t, json.Unmarshal(data, &m))
			switch field {
			case "locator":
				m.Derived[0].Entry.Locator = digest([]byte("tampered"))
			case "link":
				m.Derived[0].Entry.Links = []string{"forged"}
			case "diff_digest":
				for i := range m.Derived {
					if m.Derived[i].Entry.Kind == ComponentDiff {
						m.Derived[i].Entry.Digest = digest([]byte("other"))
					}
				}
			case "semantic_reference":
				for i := range m.Semantic.Entries {
					if m.Semantic.Entries[i].Entry.Kind == ComponentDiff {
						m.Semantic.Entries[i].Changes[0].BeforeKey = "other"
					}
				}
			}
			data, err = json.Marshal(m)
			require.NoError(t, err)
			if field == "unknown_field" {
				var obj map[string]any
				require.NoError(t, json.Unmarshal(data, &obj))
				obj["unexpected"] = true
				data, err = json.Marshal(obj)
				require.NoError(t, err)
			}
			require.NoError(t, os.WriteFile(path, data, 0600))
			_, err = f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{Editor, "editor/v1"})
			assert.ErrorIs(t, err, ErrIntegrity)
		})
	}
}

func TestDiffBodyTamperingAndMissingBlob(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	diff := entryOf(t, rows, ComponentDiff)
	body, err := v.Read(t.Context(), ReadQuery{Locator: diff.Locator})
	require.NoError(t, err)
	var changes []ComponentChange
	require.NoError(t, json.Unmarshal(body.Data, &changes))
	require.Len(t, changes, 1)
	assert.NotEmpty(t, changes[0].BeforeLocator)
	assert.NotEmpty(t, changes[0].AfterLocator)
	path := filepath.Join(f.root, "blobs", diff.Digest)
	require.NoError(t, os.WriteFile(path, []byte("corruption"), 0600))
	body, err = v.Read(t.Context(), ReadQuery{Locator: diff.Locator})
	require.NoError(t, err)
	assert.Equal(t, Missing, body.Entry.State)
	assert.Equal(t, "integrity_failure", body.Entry.Code)
	require.NoError(t, os.Remove(path))
	body, err = v.Read(t.Context(), ReadQuery{Locator: diff.Locator})
	require.NoError(t, err)
	assert.Equal(t, "missing_content", body.Entry.Code)
}
