package candidate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

func freezeBaseline(t *testing.T, s *candidate.Store) *candidate.Frozen {
	t.Helper()
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	f, err := s.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	return f
}

func TestFrozenIdentityOwnsBytesAndCanonicalOrdering(t *testing.T) {
	inputs := []candidate.Component{
		{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("Use helper"), Dependencies: []string{"helper.go"}},
		{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("package main")},
	}
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), inputs)
	require.NoError(t, err)
	f := freezeBaseline(t, s)
	inputs[0].Data[0] = 'X'
	got, err := f.Read("skill.md")
	require.NoError(t, err)
	assert.Equal(t, "Use helper", string(got))
	got[0] = 'Y'
	f.Components()[0].Data[0] = 'Z'
	manifest := f.Manifest()
	manifest.Components[0].Name = "changed"
	data, err := f.Read("skill.md")
	require.NoError(t, err)
	assert.Equal(t, "Use helper", string(data))
	reverse := []candidate.Component{{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("package main")}, {Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("Use helper"), Dependencies: []string{"helper.go"}}}
	other, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), reverse)
	require.NoError(t, err)
	assert.Equal(t, f.Digest(), freezeBaseline(t, other).Digest())
	closure, err := f.Closure([]string{"skill.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"helper.go", "skill.md"}, closure)
	_, err = f.Read("absent")
	require.ErrorIs(t, err, candidate.ErrNotFound)
	_, err = f.Closure([]string{"absent"})
	require.ErrorIs(t, err, candidate.ErrNotFound)
}

func TestFrozenIdentityBindsSurfaceBaselineAndContent(t *testing.T) {
	original := freezeBaseline(t, openStore(t)).Digest()
	for _, tt := range []struct {
		name   string
		change func(*candidate.Surface, *[]candidate.Component)
	}{
		{"binding", func(s *candidate.Surface, _ *[]candidate.Component) {
			s.Slots[0].Binding.Version = "2"
		}},
		{"surface", func(s *candidate.Surface, _ *[]candidate.Component) {
			s.Version = "2"
		}},
		{"limits", func(s *candidate.Surface, _ *[]candidate.Component) {
			s.MaxTotalBytes = 2048
		}},
		{"bytes", func(_ *candidate.Surface, b *[]candidate.Component) {
			(*b)[0].Data = []byte("different")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			surface := testSurface()
			b := baseline()
			tt.change(&surface, &b)
			s, err := candidate.Open(t.Context(), t.TempDir(), surface, b)
			require.NoError(t, err)
			assert.NotEqual(t, original, freezeBaseline(t, s).Digest())
		})
	}
}

func TestCandidateRejectsInvalidComponentBatches(t *testing.T) {
	cases := []struct {
		name       string
		components []candidate.Component
	}{
		{"unknown slot", []candidate.Component{{Name: "x", Slot: "plugins", MediaType: "text/plain", Data: []byte("x")}}},
		{"traversal", []candidate.Component{{Name: "../x", Slot: "skills", MediaType: "text/plain"}}},
		{"absolute", []candidate.Component{{Name: "/x", Slot: "skills", MediaType: "text/plain"}}},
		{"unnormalized", []candidate.Component{{Name: "a/../x", Slot: "skills", MediaType: "text/plain"}}},
		{"backslash", []candidate.Component{{Name: `a\x`, Slot: "skills", MediaType: "text/plain"}}},
		{"media", []candidate.Component{{Name: "x", Slot: "skills", MediaType: "application/json"}}},
		{"missing dependency", []candidate.Component{{Name: "x", Slot: "skills", MediaType: "text/plain", Dependencies: []string{"missing"}}}},
		{"duplicate", append(baseline(), baseline()...)},
		{"duplicate dependency", []candidate.Component{{Name: "x", Slot: "skills", MediaType: "text/plain", Dependencies: []string{"x", "x"}}}},
		{"size", []candidate.Component{{Name: "x", Slot: "skills", MediaType: "text/plain", Data: make([]byte, 513)}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), tt.components)
			assert.Error(t, err)
		})
	}
}
