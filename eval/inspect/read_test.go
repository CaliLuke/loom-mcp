package inspect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundsUseServerCeilingsAndRejectInvalidOffsets(t *testing.T) {
	f := newFixture(t)
	f.w.limits.MaxReadBytes = 2
	f.w.limits.MaxPageEntries = 1
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, err := f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{Editor, "editor/v1"})
	require.NoError(t, err)
	page, err := v.List(t.Context(), ListQuery{Limit: 99999})
	require.NoError(t, err)
	assert.Len(t, page.Entries, 1)
	assert.True(t, page.Truncated)
	source := ""
	for offset := 0; offset < page.Total; offset++ {
		p, err := v.List(t.Context(), ListQuery{Offset: offset})
		require.NoError(t, err)
		if p.Entries[0].Kind == TaskSource {
			source = p.Entries[0].Locator
		}
	}
	require.NotEmpty(t, source)
	result, err := v.Read(t.Context(), ReadQuery{Locator: source, MaxBytes: 99999})
	require.NoError(t, err)
	assert.Equal(t, "ab", string(result.Data))
	assert.True(t, result.Truncated)
	for _, q := range []ReadQuery{{Locator: source, Offset: -1}, {Locator: source, MaxBytes: -1}, {Locator: source, Offset: 999}} {
		_, err := v.Read(t.Context(), q)
		require.ErrorIs(t, err, ErrInvalidInput)
	}
	for _, q := range []ListQuery{{Offset: -1}, {Limit: -1}, {Offset: 99999}} {
		_, err := v.List(t.Context(), q)
		require.ErrorIs(t, err, ErrInvalidInput)
	}
	end, err := v.List(t.Context(), ListQuery{Offset: page.Total})
	require.NoError(t, err)
	assert.Empty(t, end.Entries)
	assert.False(t, end.Truncated)
}

func TestPublicationLimitsAndCancellation(t *testing.T) {
	f := newFixture(t)
	f.w.limits.MaxEntries = 1
	_, err := f.w.Publish(t.Context(), f.pub)
	require.ErrorIs(t, err, ErrInvalidInput)
	f.w.limits = DefaultLimits()
	f.w.limits.MaxArtifactBytes = 2
	_, err = f.w.Publish(t.Context(), f.pub)
	require.ErrorIs(t, err, ErrInvalidInput)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.w.Publish(ctx, f.pub)
	require.ErrorIs(t, err, context.Canceled)
	f.w.limits = DefaultLimits()
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, err := f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{Editor, "editor/v1"})
	require.NoError(t, err)
	_, err = v.List(ctx, ListQuery{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestViewNeverFollowsSymlinkedManifestOrBlob(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	v, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	source := entryOf(t, rows, TaskSource)
	target := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(target, []byte("outside"), 0600))
	blob := filepath.Join(f.root, "blobs", source.Digest)
	require.NoError(t, os.Remove(blob))
	require.NoError(t, os.Symlink(target, blob))
	got, err := v.Read(t.Context(), ReadQuery{Locator: source.Locator})
	require.NoError(t, err)
	assert.Empty(t, got.Data)
	assert.Equal(t, "integrity_failure", got.Entry.Code)
	path := filepath.Join(f.root, "manifests", string(ref)+".json")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(target, path))
	_, err = f.w.View(Scope{[]ManifestRef{ref}}, ViewPolicy{Editor, "editor/v1"})
	require.ErrorIs(t, err, ErrIntegrity)
}

func TestPublicationIdentityIdempotentAndViewsAreFinite(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	same, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	assert.Equal(t, ref, same)
	first, entries := entriesFor(t, f, ref, Editor, "editor/v1")
	f.pub.Attachments[0].Data = []byte("new source")
	next, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	assert.NotEqual(t, ref, next)
	_, others := entriesFor(t, f, next, Editor, "editor/v1")
	_, err = first.Read(t.Context(), ReadQuery{Locator: entryOf(t, others, TaskSource).Locator})
	require.ErrorIs(t, err, ErrNotFound)
	old, err := first.Read(t.Context(), ReadQuery{Locator: entryOf(t, entries, TaskSource).Locator})
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(old.Data))
	_, err = f.w.View(Scope{[]ManifestRef{ref, ref}}, ViewPolicy{Editor, "editor/v1"})
	require.ErrorIs(t, err, ErrInvalidInput)
	empty, err := f.w.View(Scope{}, ViewPolicy{Editor, "editor/v1"})
	require.NoError(t, err)
	page, err := empty.List(t.Context(), ListQuery{})
	require.NoError(t, err)
	assert.Zero(t, page.Total)
}
