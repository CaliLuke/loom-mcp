package candidate_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyPreviewIsCompleteAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := candidate.Open(t.Context(), dir, testSurface(), baseline())
	require.NoError(t, err)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	before := storeFiles(t, dir)
	request := candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}}
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, request)
	require.NoError(t, err)
	assert.Equal(t, []string{"helper.go", "resource.txt", "skill.md"}, refNames(plan.Closure))
	assert.Equal(t, []string{"skill.md"}, plan.Replace)
	assert.Equal(t, []string{"skill.md"}, plan.Selected)
	assert.Len(t, plan.Digest, 64)
	assert.Equal(t, 1, plan.Version)
	assert.Equal(t, d.ID, plan.DraftID)
	assert.Equal(t, d.Revision, plan.Revision)
	assert.Equal(t, source.Manifest().SurfaceDigest, plan.SurfaceDigest)
	assert.Equal(t, s.Discover().BaselineDigest, plan.BaselineDigest)
	assert.Equal(t, before, storeFiles(t, dir))
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, after)
	again, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, request)
	require.NoError(t, err)
	assert.Equal(t, plan, again)
	plan.Closure[2].Dependencies[0] = "changed"
	plan.Selected[0] = "changed"
	plan.Replace[0] = "changed"
	owned, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, request)
	require.NoError(t, err)
	assert.Equal(t, again, owned)
}

func TestCopyConfirmedClosureAndOrigins(t *testing.T) {
	s := openStore(t)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	// A preexisting dependency may not substitute for the exact source helper.
	d, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("foreign")}}})
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"helper.go", "skill.md"}, plan.Replace)
	result, err := s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
	require.NoError(t, err)
	assert.Equal(t, d.Revision+1, result.Draft.Revision)
	assert.Equal(t, plan.Digest, result.PlanDigest)
	assert.Equal(t, []string{"helper.go", "resource.txt", "skill.md"}, result.Closure)
	assert.Equal(t, result.Closure, componentNames(result.Draft.Components))
	frozen, err := s.Freeze(t.Context(), d.ID, result.Draft.Revision)
	require.NoError(t, err)
	assert.Equal(t, 2, frozen.Manifest().Version)
	require.Len(t, frozen.Manifest().Origins, 3)
	assert.Equal(t, source.Digest(), frozen.Manifest().Origins[0].SourceCandidate)
	helper, err := frozen.Read("helper.go")
	require.NoError(t, err)
	assert.Equal(t, "submitted helper", string(helper))
	result.Draft.Components[0].Data[0] = 'X'
	result.Draft.Origins[0].SourceCandidate = "changed"
	manifest := frozen.Manifest()
	manifest.Origins[0].SourceCandidate = "changed"
	loaded, err := s.Load(t.Context(), frozen.Digest())
	require.NoError(t, err)
	assert.Equal(t, frozen.Manifest(), loaded.Manifest())
	assert.Equal(t, source.Digest(), loaded.Manifest().Origins[0].SourceCandidate)
	_, err = s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
	require.ErrorIs(t, err, candidate.ErrRevision)
}

func TestCopyConfirmationRejectsChangedInputsAtomically(t *testing.T) {
	cases := []struct {
		name   string
		change func(*candidate.CopyRequest)
	}{
		{"missing digest", func(r *candidate.CopyRequest) {
			r.PlanDigest = ""
		}},
		{"wrong digest", func(r *candidate.CopyRequest) {
			r.PlanDigest = strings.Repeat("a", 64)
		}},
		{"different selection", func(r *candidate.CopyRequest) {
			r.Components = []string{"helper.go"}
		}},
		{"unconfirmed collision", func(r *candidate.CopyRequest) {
			r.Replace = nil
		}},
		{"extra replacement", func(r *candidate.CopyRequest) {
			r.Replace = append(r.Replace, "helper.go")
		}},
		{"duplicate replacement", func(r *candidate.CopyRequest) {
			r.Replace = append(r.Replace, "skill.md")
		}},
		{"duplicate selection", func(r *candidate.CopyRequest) {
			r.Components = append(r.Components, "skill.md")
		}},
		{"different source", func(r *candidate.CopyRequest) {
			r.SourceCandidate = "baseline"
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			source := reuseSource(t, s)
			d, err := s.Create(t.Context())
			require.NoError(t, err)
			plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
			require.NoError(t, err)
			request := confirmedCopy(plan)
			tt.change(&request)
			_, err = s.Copy(t.Context(), d.ID, d.Revision, request)
			require.Error(t, err)
			after, err := s.Draft(t.Context(), d.ID)
			require.NoError(t, err)
			assert.Equal(t, d, after)
		})
	}
}

func TestCopyPlanBindsDestinationRevisionAndIdentity(t *testing.T) {
	s := openStore(t)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	other, err := s.Create(t.Context())
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.NoError(t, err)
	_, err = s.Copy(t.Context(), other.ID, other.Revision, confirmedCopy(plan))
	require.ErrorIs(t, err, candidate.ErrCopyPlan)
	updated, err := s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Put, Component: baseline()[0]}})
	require.NoError(t, err)
	_, err = s.Copy(t.Context(), d.ID, updated.Revision, confirmedCopy(plan))
	require.ErrorIs(t, err, candidate.ErrCopyPlan)
	_, err = s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
	require.ErrorIs(t, err, candidate.ErrRevision)
}

func TestCopyPreviewRejectsInvalidSourcesSelectionsAndFrozenDraft(t *testing.T) {
	s := openStore(t)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	cases := []candidate.PreviewCopyRequest{
		{SourceCandidate: source.Digest()},
		{SourceCandidate: source.Digest(), Components: []string{"skill.md", "skill.md"}},
		{SourceCandidate: source.Digest(), Components: []string{"../escape"}},
		{SourceCandidate: source.Digest(), Components: []string{"absent"}},
		{SourceCandidate: strings.Repeat("a", 64), Components: []string{"skill.md"}},
		{SourceCandidate: "../source", Components: []string{"skill.md"}},
	}
	for _, request := range cases {
		_, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, request)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.PreviewCopy(ctx, d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Copy(ctx, d.ID, d.Revision, candidate.CopyRequest{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.ErrorIs(t, err, candidate.ErrFrozen)
}

func TestCopyRechecksHostValidation(t *testing.T) {
	valid := true
	validator := func(context.Context, []candidate.Component) error {
		if !valid {
			return errors.New("activation denied")
		}
		return nil
	}
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), baseline(), validator)
	require.NoError(t, err)
	source := reuseSource(t, s)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	plan, err := s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.NoError(t, err)
	valid = false
	_, err = s.PreviewCopy(t.Context(), d.ID, d.Revision, candidate.PreviewCopyRequest{SourceCandidate: source.Digest(), Components: []string{"skill.md"}})
	require.ErrorContains(t, err, "activation")
	_, err = s.Copy(t.Context(), d.ID, d.Revision, confirmedCopy(plan))
	require.ErrorContains(t, err, "activation")
	after, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, after)
}

func reuseSource(t *testing.T, s *candidate.Store) *candidate.Frozen {
	t.Helper()
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	d, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{
		{Operation: candidate.Put, Component: candidate.Component{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("load and execute helper"), Dependencies: []string{"helper.go", "resource.txt"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("submitted helper"), Dependencies: []string{"resource.txt"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "resource.txt", Slot: "skills", MediaType: "text/plain", Data: []byte("resource")}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "unrelated.md", Slot: "skills", MediaType: "text/plain", Data: []byte("not selected")}},
	})
	require.NoError(t, err)
	f, err := s.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	return f
}

func confirmedCopy(plan candidate.CopyPlan) candidate.CopyRequest {
	return candidate.CopyRequest{SourceCandidate: plan.SourceCandidate, Components: plan.Selected, Replace: plan.Replace, PlanDigest: plan.Digest}
}

func refNames(refs []candidate.ComponentRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

func componentNames(components []candidate.Component) []string {
	names := make([]string, 0, len(components))
	for _, c := range components {
		names = append(names, c.Name)
	}
	return names
}

func storeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		data, err := root.ReadFile(entry.Name())
		require.NoError(t, err)
		files[entry.Name()] = string(data)
	}
	return files
}
