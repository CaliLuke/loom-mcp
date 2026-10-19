// Package candidate stores versioned, immutable candidate content without runtime execution dependencies.
package candidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"unicode"
)

// Component is a byte submission, never a reference to a live file or directory.
type Component struct {
	Name         string   `json:"name"`
	Slot         string   `json:"slot"`
	MediaType    string   `json:"media_type"`
	Data         []byte   `json:"data"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// ComponentRef records the identity and explicit dependency edges of one component.
type ComponentRef struct {
	Name         string   `json:"name"`
	Slot         string   `json:"slot"`
	MediaType    string   `json:"media_type"`
	Digest       string   `json:"digest"`
	Size         int64    `json:"size"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// Manifest is the canonical versioned identity of a complete content snapshot.
type Manifest struct {
	Version        int               `json:"version"`
	SurfaceID      string            `json:"surface_id"`
	SurfaceVersion string            `json:"surface_version"`
	SurfaceDigest  string            `json:"surface_digest"`
	BaselineDigest string            `json:"baseline_digest,omitempty"`
	Bindings       []Binding         `json:"bindings"`
	Components     []ComponentRef    `json:"components"`
	Origins        []ComponentOrigin `json:"origins,omitempty"`
}

// Frozen retains immutable content; all accessors return owned values.
type Frozen struct {
	manifest   Manifest
	components []Component
	digest     string
}

// Draft is a complete editable snapshot and optimistic revision.
type Draft struct {
	ID           string            `json:"id"`
	Revision     uint64            `json:"revision"`
	FrozenDigest string            `json:"frozen_digest,omitempty"`
	Components   []Component       `json:"components"`
	Origins      []ComponentOrigin `json:"origins,omitempty"`
}

// Edit declares exactly one named put or delete operation.
type Edit struct {
	Operation Operation
	Name      string
	Component Component
}

var (
	// ErrRevision reports an optimistic revision conflict.
	ErrRevision = errors.New("candidate: stale revision")
	// ErrFrozen reports an attempted edit to a frozen draft.
	ErrFrozen = errors.New("candidate: draft is frozen")
	// ErrNotFound reports an unknown draft, snapshot or component.
	ErrNotFound = errors.New("candidate: not found")
)

// Digest returns the canonical SHA-256 manifest digest.
func (f *Frozen) Digest() string {
	return f.digest
}

// Manifest returns an owned manifest.
func (f *Frozen) Manifest() Manifest {
	manifest := f.manifest
	manifest.Origins = slices.Clone(manifest.Origins)
	manifest.Bindings = slices.Clone(manifest.Bindings)
	manifest.Components = slices.Clone(manifest.Components)
	for i := range manifest.Components {
		manifest.Components[i].Dependencies = slices.Clone(manifest.Components[i].Dependencies)
	}
	return manifest
}

// Components returns owned bytes and metadata.
func (f *Frozen) Components() []Component {
	return cloneComponents(f.components)
}

// Read returns the named component bytes.
func (f *Frozen) Read(name string) ([]byte, error) {
	for _, component := range f.components {
		if component.Name == name {
			return slices.Clone(component.Data), nil
		}
	}
	return nil, fmt.Errorf("%w: component %q", ErrNotFound, name)
}

// Closure returns sorted transitive dependencies including the selected names.
func (f *Frozen) Closure(names []string) ([]string, error) {
	components := map[string]Component{}
	for _, component := range f.components {
		components[component.Name] = component
	}
	visited := map[string]bool{}
	pending := slices.Clone(names)
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		component, ok := components[name]
		if !ok {
			return nil, fmt.Errorf("%w: component %q", ErrNotFound, name)
		}
		if visited[name] {
			continue
		}
		visited[name] = true
		pending = append(pending, component.Dependencies...)
	}
	result := make([]string, 0, len(visited))
	for name := range visited {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}

// makeFrozen binds canonical content and the entire surface to immutable identity.
func makeFrozen(surface Surface, baseline string, components []Component) (*Frozen, error) {
	return makeFrozenWithOrigins(surface, baseline, components, nil)
}

// makeFrozenWithOrigins preserves v1 identity unless the snapshot has copy origins.
func makeFrozenWithOrigins(surface Surface, baseline string, components []Component, origins []ComponentOrigin) (*Frozen, error) {
	surfaceBytes, err := json.Marshal(surface)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Version: 1, SurfaceID: surface.ID, SurfaceVersion: surface.Version, SurfaceDigest: hashBytes(surfaceBytes), BaselineDigest: baseline, Bindings: []Binding{}, Components: []ComponentRef{}}
	seen := map[Binding]bool{}
	for _, slot := range surface.Slots {
		if !seen[slot.Binding] {
			manifest.Bindings = append(manifest.Bindings, slot.Binding)
			seen[slot.Binding] = true
		}
	}
	slices.SortFunc(manifest.Bindings, func(a, b Binding) int {
		return strings.Compare(a.ID, b.ID)
	})
	components = cloneComponents(components)
	slices.SortFunc(components, func(a, b Component) int {
		return strings.Compare(a.Name, b.Name)
	})
	for i := range components {
		c := &components[i]
		if len(c.Dependencies) == 0 {
			c.Dependencies = nil
		}
		slices.Sort(c.Dependencies)
		manifest.Components = append(manifest.Components, ComponentRef{Name: c.Name, Slot: c.Slot, MediaType: c.MediaType, Digest: hashBytes(c.Data), Size: int64(len(c.Data)), Dependencies: slices.Clone(c.Dependencies)})
	}
	if len(origins) > 0 {
		manifest.Version = 2
		manifest.Origins = slices.Clone(origins)
		slices.SortFunc(manifest.Origins, func(a, b ComponentOrigin) int {
			return strings.Compare(a.Name, b.Name)
		})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return &Frozen{manifest: manifest, components: components, digest: hashBytes(encoded)}, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validName(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, `\:`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func cloneComponents(components []Component) []Component {
	result := slices.Clone(components)
	for i := range result {
		result[i].Data = slices.Clone(result[i].Data)
		result[i].Dependencies = slices.Clone(result[i].Dependencies)
	}
	return result
}

// validateComponents checks explicit structural dependencies; it never parses prose.
func validateComponents(surface Surface, components []Component) error {
	slots := map[string]Slot{}
	for _, slot := range surface.Slots {
		slots[slot.Name] = slot
	}
	counts := map[string]int{}
	names := map[string]bool{}
	var total int64
	for _, component := range components {
		slot, ok := slots[component.Slot]
		if !ok || !validName(component.Name) || names[component.Name] {
			return fmt.Errorf("candidate: invalid component name or slot %q", component.Name)
		}
		names[component.Name] = true
		if !slices.Contains(slot.MediaTypes, component.MediaType) {
			return fmt.Errorf("candidate: unsupported media type for %q", component.Name)
		}
		size := int64(len(component.Data))
		counts[component.Slot]++
		if size > slot.MaxBytes || counts[component.Slot] > slot.MaxComponents || size > surface.MaxTotalBytes-total {
			return fmt.Errorf("candidate: content limit exceeded by %q", component.Name)
		}
		total += size
	}
	for _, component := range components {
		deps := map[string]bool{}
		for _, dependency := range component.Dependencies {
			if !names[dependency] || dependency == component.Name || deps[dependency] {
				return fmt.Errorf("candidate: missing or invalid dependency %q of %q", dependency, component.Name)
			}
			deps[dependency] = true
		}
		// A materializer cannot install a regular file above another component.
		parent := component.Name
		for strings.Contains(parent, "/") {
			parent = parent[:strings.LastIndex(parent, "/")]
			if names[parent] {
				return fmt.Errorf("candidate: component path collision at %q", parent)
			}
		}
	}
	return nil
}
