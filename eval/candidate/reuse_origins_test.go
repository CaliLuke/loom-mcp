package candidate_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyOriginsSurviveForkEditAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	source := reuseSource(t, s)
	imported := copySkill(t, s, source)
	frozen, err := s.Freeze(t.Context(), imported.ID, imported.Revision)
	require.NoError(t, err)
	reopened, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	loaded, err := reopened.Load(t.Context(), frozen.Digest())
	require.NoError(t, err)
	assert.Equal(t, frozen.Manifest(), loaded.Manifest())
	fork, err := reopened.Fork(t.Context(), frozen.Digest())
	require.NoError(t, err)
	assert.Equal(t, imported.Origins, fork.Origins)
	// An explicitly authored identical-byte put still clears that name's origin.
	fork, err = reopened.Edit(t.Context(), fork.ID, fork.Revision, []candidate.Edit{{Operation: candidate.Put, Component: fork.Components[0]}})
	require.NoError(t, err)
	assert.Equal(t, []string{"resource.txt", "skill.md"}, originNames(fork.Origins))
	fork, err = reopened.Edit(t.Context(), fork.ID, fork.Revision, []candidate.Edit{
		{Operation: candidate.Delete, Name: "skill.md"},
		{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("new helper")}},
		{Operation: candidate.Delete, Name: "resource.txt"},
	})
	require.NoError(t, err)
	assert.Empty(t, fork.Origins)
	authored, err := reopened.Freeze(t.Context(), fork.ID, fork.Revision)
	require.NoError(t, err)
	assert.Equal(t, 1, authored.Manifest().Version)
	again, err := reopened.Freeze(t.Context(), imported.ID, imported.Revision)
	require.NoError(t, err)
	assert.Equal(t, frozen.Digest(), again.Digest())
}

func TestCopyOriginsValidateOnlyImmediateSource(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	a := reuseSource(t, s)
	bDraft := copySkill(t, s, a)
	b, err := s.Freeze(t.Context(), bDraft.ID, bDraft.Revision)
	require.NoError(t, err)
	cDraft := copySkill(t, s, b)
	c, err := s.Freeze(t.Context(), cDraft.ID, cDraft.Revision)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "frozen-"+a.Digest()+".json")))
	_, err = s.Load(t.Context(), b.Digest())
	require.ErrorIs(t, err, candidate.ErrNotFound)
	loaded, err := s.Load(t.Context(), c.Digest())
	require.NoError(t, err)
	assert.Equal(t, b.Digest(), loaded.Manifest().Origins[0].SourceCandidate)
}

func TestCopyManifestRejectsForgedOriginsAndVersions(t *testing.T) {
	cases := []struct {
		name   string
		change func(*candidate.Manifest)
	}{
		{"unknown version", func(m *candidate.Manifest) {
			m.Version = 3
		}},
		{"v1 origins", func(m *candidate.Manifest) {
			m.Version = 1
		}},
		{"v2 no origins", func(m *candidate.Manifest) {
			m.Origins = nil
		}},
		{"duplicate origin", func(m *candidate.Manifest) {
			m.Origins = append(m.Origins, m.Origins[0])
		}},
		{"unsorted origins", func(m *candidate.Manifest) {
			m.Origins[0], m.Origins[1] = m.Origins[1], m.Origins[0]
		}},
		{"missing current component", func(m *candidate.Manifest) {
			m.Origins[0].Name = "absent"
		}},
		{"different source bytes", func(m *candidate.Manifest) {
			m.Origins[0].SourceDigest = m.Components[1].Digest
		}},
		{"different source snapshot", func(m *candidate.Manifest) {
			m.Origins[0].SourceCandidate = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{"invalid source digest", func(m *candidate.Manifest) {
			m.Origins[0].SourceDigest = "invalid"
		}},
		{"invalid source identity", func(m *candidate.Manifest) {
			m.Origins[0].SourceCandidate = "../source"
		}},
		{"different dependency metadata", func(m *candidate.Manifest) {
			m.Components[0].Dependencies = nil
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
			require.NoError(t, err)
			source := reuseSource(t, s)
			d := copySkill(t, s, source)
			f, err := s.Freeze(t.Context(), d.ID, d.Revision)
			require.NoError(t, err)
			var stored struct {
				Manifest   candidate.Manifest    `json:"manifest"`
				Components []candidate.Component `json:"components"`
			}
			stored.Manifest = f.Manifest()
			stored.Components = f.Components()
			tt.change(&stored.Manifest)
			if tt.name == "different dependency metadata" {
				stored.Components[0].Dependencies = nil
			}
			// Rehash the forged manifest: rejection must verify semantics, not just filename hash.
			manifestBytes, err := json.Marshal(stored.Manifest)
			require.NoError(t, err)
			sum := sha256.Sum256(manifestBytes)
			digest := hex.EncodeToString(sum[:])
			data, err := json.Marshal(stored)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "frozen-"+digest+".json"), data, 0600))
			_, err = s.Load(t.Context(), digest)
			require.Error(t, err)
		})
	}
}

func TestCopyDraftOriginTamperingIsRejected(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	source := reuseSource(t, s)
	d := copySkill(t, s, source)
	d.Origins[0].SourceDigest = "bad"
	data, err := json.Marshal(d)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "draft-"+d.ID+".json"), data, 0600))
	_, err = s.Draft(t.Context(), d.ID)
	require.Error(t, err)
	require.Error(t, s.Validate(t.Context(), d.ID, d.Revision))
	_, err = s.Freeze(t.Context(), d.ID, d.Revision)
	require.Error(t, err)
}

func copySkill(t *testing.T, s *candidate.Store, source *candidate.Frozen) candidate.Draft {
	t.Helper()
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.NoError(t, err)
	result, err := s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
	require.NoError(t, err)
	return result.Draft
}

func originNames(origins []candidate.ComponentOrigin) []string {
	names := make([]string, 0, len(origins))
	for _, origin := range origins {
		names = append(names, origin.Name)
	}
	return names
}
