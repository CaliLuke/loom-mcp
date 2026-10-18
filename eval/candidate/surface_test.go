package candidate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

func testSurface() candidate.Surface {
	return candidate.Surface{ID: "assistant", Version: "1", MaxTotalBytes: 1024, Slots: []candidate.Slot{
		{Name: "skills", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put, candidate.Delete}, MediaTypes: []string{"text/plain"}, MaxBytes: 512, MaxComponents: 4, Binding: candidate.Binding{ID: "skill-loader", Version: "1"}, Capabilities: []string{"read-only tools"}},
		{Name: "helpers", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put, candidate.Delete}, MediaTypes: []string{"text/x-go"}, MaxBytes: 512, MaxComponents: 4, Binding: candidate.Binding{ID: "go-helper", Version: "1"}},
	}}
}

func baseline() []candidate.Component {
	return []candidate.Component{{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("Standing {{template}} instructions")}}
}

func openStore(t *testing.T) *candidate.Store {
	t.Helper()
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), baseline())
	require.NoError(t, err)
	return s
}

func TestSurfaceDiscoveryOwnsRegisteredDeclaration(t *testing.T) {
	surface := testSurface()
	s, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
	require.NoError(t, err)
	surface.Slots[0].Binding.Version = "changed"
	discovery := s.Discover()
	require.Len(t, discovery.BaselineDigest, 64)
	assert.Equal(t, "1", discovery.Surface.Slots[0].Binding.Version)
	assert.Equal(t, candidate.OnDemand, discovery.Surface.Slots[0].Activation)
	discovery.Surface.Slots[0].MediaTypes[0] = "changed"
	assert.Equal(t, "text/x-go", s.Discover().Surface.Slots[0].MediaTypes[0])
}

func TestSurfaceRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		change func(*candidate.Surface)
	}{
		{"id", func(s *candidate.Surface) {
			s.ID = ""
		}},
		{"version", func(s *candidate.Surface) {
			s.Version = ""
		}},
		{"total", func(s *candidate.Surface) {
			s.MaxTotalBytes = 0
		}},
		{"bytes", func(s *candidate.Surface) {
			s.Slots[0].MaxBytes = 0
		}},
		{"count", func(s *candidate.Surface) {
			s.Slots[0].MaxComponents = 0
		}},
		{"activation", func(s *candidate.Surface) {
			s.Slots[0].Activation = "observer"
		}},
		{"binding", func(s *candidate.Surface) {
			s.Slots[0].Binding.Version = ""
		}},
		{"media", func(s *candidate.Surface) { s.Slots[0].MediaTypes = []string{"invalid"} }},
		{"operations", func(s *candidate.Surface) { s.Slots[0].Operations = []candidate.Operation{"execute"} }},
		{"duplicate", func(s *candidate.Surface) {
			s.Slots = append(s.Slots, s.Slots[0])
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			surface := testSurface()
			tt.change(&surface)
			_, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
			require.Error(t, err)
		})
	}
}

func TestHostValidationRejectsContentAndOwnsSnapshots(t *testing.T) {
	validator := func(ctx context.Context, components []candidate.Component) error {
		if string(components[0].Data) == "bad" {
			return errors.New("missing activation trigger")
		}
		components[0].Data[0] = 'X'
		return ctx.Err()
	}
	s, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), baseline(), validator)
	require.NoError(t, err)
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	assert.Equal(t, baseline(), d.Components)
	_, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "skill.md", Slot: "skills", MediaType: "text/plain", Data: []byte("bad")}}})
	require.ErrorContains(t, err, "activation")
	unchanged, err := s.Draft(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, d, unchanged)
}

func TestSurfaceRejectsInvalidMediaOperationsAndConflictingBindings(t *testing.T) {
	for _, change := range []func(*candidate.Surface){
		func(s *candidate.Surface) {
			s.Slots = nil
		},
		func(s *candidate.Surface) {
			s.Slots[0].MediaTypes = nil
		},
		func(s *candidate.Surface) { s.Slots[0].MediaTypes = []string{"text/*"} },
		func(s *candidate.Surface) { s.Slots[0].MediaTypes = []string{"text/plain", "text/plain"} },
		func(s *candidate.Surface) {
			s.Slots[0].Operations = nil
		},
		func(s *candidate.Surface) {
			s.Slots[0].Operations = []candidate.Operation{candidate.Put, candidate.Put}
		},
		func(s *candidate.Surface) { s.Slots[1].Binding = candidate.Binding{ID: "skill-loader", Version: "2"} },
	} {
		surface := testSurface()
		change(&surface)
		_, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
		require.Error(t, err)
	}
	_, err := candidate.Open(t.Context(), t.TempDir(), testSurface(), baseline(), nil)
	require.Error(t, err)
}

func TestSurfaceEnforcesDeclaredPermissionsAndAggregateLimits(t *testing.T) {
	for _, operation := range []candidate.Operation{candidate.Put, candidate.Delete} {
		t.Run(string(operation), func(t *testing.T) {
			surface := testSurface()
			surface.Slots[0].Operations = []candidate.Operation{operation}
			s, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
			require.NoError(t, err)
			d, err := s.Create(t.Context())
			require.NoError(t, err)
			forbidden := candidate.Delete
			if operation == candidate.Delete {
				forbidden = candidate.Put
			}
			_, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: forbidden, Name: "skill.md", Component: baseline()[0]}})
			require.Error(t, err)
		})
	}
	surface := testSurface()
	surface.MaxTotalBytes = 4
	_, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
	require.Error(t, err)
	surface = testSurface()
	surface.Slots[0].MaxComponents = 1
	b := append(baseline(), candidate.Component{Name: "second.md", Slot: "skills", MediaType: "text/plain"})
	_, err = candidate.Open(t.Context(), t.TempDir(), surface, b)
	require.Error(t, err)
}

func TestSurfaceRejectsNonUTF8IdentityText(t *testing.T) {
	for _, change := range []func(*candidate.Surface){
		func(s *candidate.Surface) {
			s.Version = "v\xff"
		},
		func(s *candidate.Surface) {
			s.Slots[0].Binding.ID = "binding\xff"
		},
		func(s *candidate.Surface) {
			s.Slots[0].Binding.Version = "v\xff"
		},
		func(s *candidate.Surface) { s.Slots[0].Capabilities = []string{"capability\xff"} },
	} {
		surface := testSurface()
		change(&surface)
		_, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline())
		require.Error(t, err)
	}
}
