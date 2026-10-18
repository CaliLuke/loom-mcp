package candidate_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

func TestStoreAtomicEditFreezeForkAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	sibling, err := s.Create(t.Context())
	require.NoError(t, err)
	edited, err := s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{
		{Operation: candidate.Put, Component: candidate.Component{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("Use helper"), Dependencies: []string{"helper.go"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("package main")}},
	})
	require.NoError(t, err)
	assert.Equal(t, d.Revision+1, edited.Revision)
	_, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "skill.md"}})
	require.ErrorIs(t, err, candidate.ErrRevision)
	_, err = s.Edit(t.Context(), d.ID, edited.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "helper.go"}})
	require.Error(t, err)
	require.NoError(t, s.Validate(t.Context(), d.ID, edited.Revision))
	f, err := s.Freeze(t.Context(), d.ID, edited.Revision)
	require.NoError(t, err)
	_, err = s.Edit(t.Context(), d.ID, edited.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "skill.md"}})
	require.ErrorIs(t, err, candidate.ErrFrozen)
	fork, err := s.Fork(t.Context(), f.Digest())
	require.NoError(t, err)
	assert.Equal(t, edited.Components, fork.Components)
	unchanged, err := s.Draft(t.Context(), sibling.ID)
	require.NoError(t, err)
	assert.Equal(t, baseline(), unchanged.Components)
	reopened, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	loaded, err := reopened.Load(t.Context(), f.Digest())
	require.NoError(t, err)
	assert.Equal(t, f.Components(), loaded.Components())
	gotDraft, err := reopened.Draft(t.Context(), fork.ID)
	require.NoError(t, err)
	assert.Equal(t, fork, gotDraft)
	_, err = reopened.Fork(t.Context(), "../../escape")
	require.Error(t, err)
	surface := testSurface()
	surface.Version = "2"
	_, err = candidate.Open(t.Context(), dir, surface, baseline())
	require.Error(t, err)
}

func TestStoreConcurrentRevisionHasOneWinner(t *testing.T) {
	s := openStore(t)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, body := range []string{"one", "two"} {
		wg.Go(func() {
			_, err := s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte(body)}}})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, candidate.ErrRevision)
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
}

func TestStoreRejectsCorruptAndSymlinkedSnapshots(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "symlink"}[symlink], func(t *testing.T) {
			dir := t.TempDir()
			s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
			require.NoError(t, err)
			f := freezeBaseline(t, s)
			target := filepath.Join(dir, "frozen-"+f.Digest()+".json")
			require.NoError(t, os.Remove(target))
			if symlink {
				other := filepath.Join(t.TempDir(), "other")
				require.NoError(t, os.WriteFile(other, []byte("{}"), 0600))
				require.NoError(t, os.Symlink(other, target))
			} else {
				require.NoError(t, os.WriteFile(target, []byte("{}"), 0600))
			}
			_, err = s.Load(t.Context(), f.Digest())
			require.Error(t, err)
		})
	}
}

func TestStoreCanceledMutationDoesNotChangeRevision(t *testing.T) {
	s := openStore(t)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Edit(ctx, d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "skill.md"}})
	require.ErrorIs(t, err, context.Canceled)
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, after)
}

func TestStoreDeleteAndBatchFailurePreserveCompleteRevision(t *testing.T) {
	s := openStore(t)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	for _, edits := range [][]candidate.Edit{
		{},
		{{Operation: "execute", Name: "skill.md"}},
		{{Operation: candidate.Put, Name: "other", Component: baseline()[0]}},
		{{Operation: candidate.Delete, Name: "absent"}},
		{{Operation: candidate.Delete, Name: "skill.md"}, {Operation: candidate.Delete, Name: "skill.md"}},
	} {
		_, err := s.Edit(t.Context(), d.ID, d.Revision, edits)
		require.Error(t, err)
	}
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, after)
	deleted, err := s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "skill.md"}})
	require.NoError(t, err)
	assert.Empty(t, deleted.Components)
	frozen, err := s.Freeze(t.Context(), d.ID, deleted.Revision)
	require.NoError(t, err)
	again, err := s.Freeze(t.Context(), d.ID, deleted.Revision)
	require.NoError(t, err)
	assert.Equal(t, frozen.Digest(), again.Digest())
	_, err = s.Freeze(t.Context(), d.ID, d.Revision)
	require.ErrorIs(t, err, candidate.ErrRevision)
}

func TestStoreRejectsMissingScopedIdentitiesAndCanceledReads(t *testing.T) {
	s := openStore(t)
	_, err := s.Draft(t.Context(), strings.Repeat("a", 32))
	require.ErrorIs(t, err, candidate.ErrNotFound)
	_, err = s.Draft(t.Context(), "../escape")
	require.ErrorIs(t, err, candidate.ErrNotFound)
	_, err = s.Load(t.Context(), strings.Repeat("a", 64))
	require.ErrorIs(t, err, candidate.ErrNotFound)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Create(ctx)
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Fork(ctx, s.Discover().BaselineDigest)
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Draft(ctx, "absent")
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Load(ctx, s.Discover().BaselineDigest)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, s.Validate(ctx, "absent", 1), context.Canceled)
	_, err = s.Freeze(ctx, "absent", 1)
	require.ErrorIs(t, err, context.Canceled)
	_, err = candidate.Open(ctx, t.TempDir(), testSurface(), baseline())
	require.ErrorIs(t, err, context.Canceled)
}

func TestStoreFreezeRevalidatesHostSemantics(t *testing.T) {
	valid := true
	validator := func(context.Context, []candidate.Component) error {
		if !valid {
			return errors.New("activation invalid")
		}
		return nil
	}
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), baseline(), validator)
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	valid = false
	require.ErrorContains(t, s.Validate(t.Context(), d.ID, d.Revision), "activation")
	_, err = s.Freeze(t.Context(), d.ID, d.Revision)
	require.ErrorContains(t, err, "activation")
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Empty(t, after.FrozenDigest)
}

func TestStoreFrozenDigestDetectsChangedBytesAndMissingEvidence(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	f := freezeBaseline(t, s)
	name := filepath.Join(dir, "frozen-"+f.Digest()+".json")
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	data, err := root.ReadFile(filepath.Base(name))
	require.NoError(t, err)
	corrupted := bytes.Replace(data, []byte(base64.StdEncoding.EncodeToString(baseline()[0].Data)), []byte(base64.StdEncoding.EncodeToString([]byte("changed"))), 1)
	require.NotEqual(t, data, corrupted)
	require.NoError(t, root.WriteFile(filepath.Base(name), corrupted, 0600))
	_, err = s.Load(t.Context(), f.Digest())
	require.ErrorContains(t, err, "mismatch")
	require.NoError(t, os.Remove(name))
	_, err = s.Load(t.Context(), f.Digest())
	require.ErrorIs(t, err, candidate.ErrNotFound)
}

func TestStoreByteSubmissionSurvivesChangedSourceDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "skill.md")
	require.NoError(t, os.WriteFile(source, []byte("original"), 0600))
	sourceRoot, err := os.OpenRoot(filepath.Dir(source))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sourceRoot.Close()) })
	data, err := sourceRoot.ReadFile("skill.md")
	require.NoError(t, err)
	b := baseline()
	b[0].Data = data
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), b)
	require.NoError(t, err)
	data[0] = 'X'
	require.NoError(t, os.WriteFile(source, []byte("changed"), 0600))
	f := freezeBaseline(t, s)
	got, err := f.Read("skill.md")
	require.NoError(t, err)
	assert.Equal(t, "original", string(got))
}

func TestStoreRejectsSymlinkRootAndRegistration(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(dir, link))
	_, err := candidate.Open(t.Context(), link, testSurface(), baseline())
	require.Error(t, err)
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "absent"), filepath.Join(dir, "registration.json")))
	_, err = candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.Error(t, err)
}

func TestStoreCanonicalEmptyMetadataReopens(t *testing.T) {
	dir := t.TempDir()
	surface := testSurface()
	surface.Slots[0].Capabilities = []string{}
	b := baseline()
	b[0].Dependencies = []string{}
	s, err := candidate.Open(t.Context(), dir, surface, b)
	require.NoError(t, err)
	f := freezeBaseline(t, s)
	reopened, err := candidate.Open(t.Context(), dir, surface, b)
	require.NoError(t, err)
	loaded, err := reopened.Load(t.Context(), f.Digest())
	require.NoError(t, err)
	assert.Equal(t, f.Manifest(), loaded.Manifest())
}

func TestStoreRelativeDirectorySurvivesWorkingDirectoryChange(t *testing.T) {
	original := t.TempDir()
	other := t.TempDir()
	t.Chdir(original)
	s, err := candidate.Open(t.Context(), "store", testSurface(), baseline())
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(other, "store"), 0700))
	t.Chdir(other)

	got, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, got)
	created, err := s.Create(t.Context())
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(original, "store", "draft-"+created.ID+".json"))
	assert.NoFileExists(t, filepath.Join(other, "store", "draft-"+created.ID+".json"))
	reopened, err := candidate.Open(t.Context(), filepath.Join(original, "store"), testSurface(), baseline())
	require.NoError(t, err)
	retained, err := reopened.Draft(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, retained)
}

func TestStoreFrozenDraftRequiresMatchingRetainedSnapshot(t *testing.T) {
	for _, corruption := range []string{"draft bytes", "draft digest", "missing snapshot", "corrupt snapshot"} {
		t.Run(corruption, func(t *testing.T) {
			dir := t.TempDir()
			s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
			require.NoError(t, err)
			d, err := s.Create(t.Context())
			require.NoError(t, err)
			f, err := s.Freeze(t.Context(), d.ID, d.Revision)
			require.NoError(t, err)
			frozenDraft, err := s.Draft(t.Context(), d.ID)
			require.NoError(t, err)
			root, err := os.OpenRoot(dir)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, root.Close())
			})
			snapshotName := "frozen-" + f.Digest() + ".json"
			switch corruption {
			case "draft bytes":
				frozenDraft.Components[0].Data = []byte("changed")
			case "draft digest":
				frozenDraft.FrozenDigest = s.Discover().BaselineDigest
			case "missing snapshot":
				require.NoError(t, root.Remove(snapshotName))
			case "corrupt snapshot":
				require.NoError(t, root.WriteFile(snapshotName, []byte("{}"), 0600))
			}
			data, err := json.Marshal(frozenDraft)
			require.NoError(t, err)
			require.NoError(t, root.WriteFile("draft-"+d.ID+".json", data, 0600))

			_, err = s.Draft(t.Context(), d.ID)
			require.Error(t, err)
			require.Error(t, s.Validate(t.Context(), d.ID, d.Revision))
			_, err = s.Freeze(t.Context(), d.ID, d.Revision)
			require.Error(t, err)
		})
	}
}
