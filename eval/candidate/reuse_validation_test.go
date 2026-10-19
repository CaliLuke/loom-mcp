package candidate_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyPlanCanonicalIdentityAndNormalizedSelection(t *testing.T) {
	s := openStore(t)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	first, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md", "helper.go"}})
	require.NoError(t, err)
	second, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"helper.go", "skill.md"}})
	require.NoError(t, err)
	assert.Equal(t, first, second)
	// Pin the external serialized contract independently of implementation helpers.
	canonical, err := json.Marshal(struct {
		Domain            string                   `json:"domain"`
		Version           int                      `json:"version"`
		DraftID           string                   `json:"draft_id"`
		Revision          uint64                   `json:"revision"`
		DestinationDigest string                   `json:"destination_digest"`
		SurfaceDigest     string                   `json:"surface_digest"`
		BaselineDigest    string                   `json:"baseline_digest"`
		SourceCandidate   string                   `json:"source_candidate"`
		Selected          []string                 `json:"selected"`
		Closure           []candidate.ComponentRef `json:"closure"`
		Replace           []string                 `json:"replace"`
	}{"loom.candidate.copy", 1, d.ID, d.Revision, first.DestinationDigest, first.SurfaceDigest, first.BaselineDigest, source.Digest(), []string{"helper.go", "skill.md"}, first.Closure, []string{"skill.md"}})
	require.NoError(t, err)
	sum := sha256.Sum256(canonical)
	assert.Equal(t, hex.EncodeToString(sum[:]), first.Digest)
}

func TestCopyPreviewRejectsPrefixCollisionAndAggregateLimits(t *testing.T) {
	for _, name := range []string{"prefix collision", "aggregate count"} {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			source := reuseSource(t, s)
			d, err := s.Create(t.Context())
			require.NoError(t, err)
			var edits []candidate.Edit
			if name == "prefix collision" {
				edits = []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go/nested", Slot: "helpers", MediaType: "text/x-go", Data: []byte("protected")}}}
			} else {
				edits = []candidate.Edit{
					{Operation: candidate.Put, Component: candidate.Component{Name: "a", Slot: "skills", MediaType: "text/plain"}},
					{Operation: candidate.Put, Component: candidate.Component{Name: "b", Slot: "skills", MediaType: "text/plain"}},
					{Operation: candidate.Put, Component: candidate.Component{Name: "c", Slot: "skills", MediaType: "text/plain"}},
				}
			}
			d, err = s.Edit(t.Context(), d.ID, d.Revision, edits)
			require.NoError(t, err)
			_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
			require.Error(t, err)
			after, err := s.Draft(t.Context(), d.ID)
			require.NoError(t, err)
			assert.Equal(t, d, after)
		})
	}
}

func TestCopyPreviewEnforcesPutPermission(t *testing.T) {
	surface := testSurface()
	surface.Slots[0].Operations = []candidate.Operation{candidate.Delete}
	s, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: s.Discover().BaselineDigest, Components: []string{"skill.md"}})
	require.ErrorContains(t, err, "unsupported")
}

func TestCopyPreviewRejectsForeignSurfaceSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	otherDir := t.TempDir()
	surface := testSurface()
	surface.Slots[0].Binding.Version = "foreign"
	foreign, err := candidate.Open(t.Context(), otherDir, surface, baseline())
	require.NoError(t, err)
	source := freezeBaseline(t, foreign)
	foreignRoot, err := os.OpenRoot(otherDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, foreignRoot.Close())
	})
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	data, err := foreignRoot.ReadFile("frozen-" + source.Digest() + ".json")
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("frozen-"+source.Digest()+".json", data, 0600))
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.Error(t, err)
}

func TestCopyPreviewRejectsExhaustedRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	d.Revision = ^uint64(0)
	data, err := json.Marshal(d)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "draft-"+d.ID+".json"), data, 0600))
	_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: s.Discover().BaselineDigest, Components: []string{"skill.md"}})
	require.ErrorContains(t, err, "exhausted")
}

func TestCopyConcurrentConfirmationsMutateOnce(t *testing.T) {
	s := openStore(t)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			_, copyErr := s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
			results <- copyErr
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, candidate.ErrRevision):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", result)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.Revision+1, after.Revision)
}

func TestCopyClosureHandlesCyclesAndEmptyReplacementSet(t *testing.T) {
	s := openStore(t)
	sourceDraft, err := s.Create(t.Context())
	require.NoError(t, err)
	sourceDraft, err = s.Edit(t.Context(), sourceDraft.ID, sourceDraft.Revision, []candidate.Edit{
		{Operation: candidate.Put, Component: candidate.Component{Name: "a.go", Slot: "helpers", MediaType: "text/x-go", Dependencies: []string{"b.go"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "b.go", Slot: "helpers", MediaType: "text/x-go", Dependencies: []string{"a.go"}}},
	})
	require.NoError(t, err)
	source, err := s.Freeze(t.Context(), sourceDraft.ID, sourceDraft.Revision)
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"a.go"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go", "b.go"}, refNames(plan.Closure))
	assert.Equal(t, []string{}, plan.Replace)
	request := confirmedCopy(plan)
	request.Replace = nil // Go nil and empty both explicitly confirm no collisions.
	result, err := s.Copy(t.Context(), d.ID, d.Revision, request)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go", "b.go", "skill.md"}, componentNames(result.Draft.Components))
	assert.Equal(t, []string{"a.go", "b.go"}, originNames(result.Draft.Origins))
}
