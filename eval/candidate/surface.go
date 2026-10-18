package candidate

import (
	"context"
	"fmt"
	"mime"
	"slices"
	"strings"
	"unicode/utf8"
)

// Activation describes when a host binding exposes a component.
type Activation string

// Operation is a permitted slot mutation.
type Operation string

// Binding identifies a host installation contract; it is not executable code.
type Binding struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Slot declares an application capability and its content limits.
type Slot struct {
	Name          string      `json:"name"`
	Activation    Activation  `json:"activation"`
	Operations    []Operation `json:"operations"`
	MediaTypes    []string    `json:"media_types"`
	MaxBytes      int64       `json:"max_bytes"`
	MaxComponents int         `json:"max_components"`
	Binding       Binding     `json:"binding"`
	Capabilities  []string    `json:"capabilities,omitempty"`
}

// Surface is the versioned host declaration of editable content.
type Surface struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	MaxTotalBytes int64  `json:"max_total_bytes"`
	Slots         []Slot `json:"slots"`
}

// Discovery describes the registered surface and its authorized baseline.
type Discovery struct {
	Surface        Surface        `json:"surface"`
	BaselineDigest string         `json:"baseline_digest"`
	Baseline       []ComponentRef `json:"baseline"`
}

// Validator checks host-specific content, activation and semantic dependencies.
// Each invocation receives an owned snapshot. It must not reenter the Store.
type Validator func(context.Context, []Component) error

const (
	// AlwaysOn exposes standing instructions.
	AlwaysOn Activation = "always_on"
	// OnDemand exposes discoverable resources when requested.
	OnDemand Activation = "on_demand"
	// ChildContext exposes instructions in an approved child context.
	ChildContext Activation = "child_context"
	// Inline installs supported behavior in the execution loop.
	Inline Activation = "inline"
	// Put adds or replaces a component.
	Put Operation = "put"
	// Delete removes a component.
	Delete Operation = "delete"
)

// normalizeSurface validates the host contract and orders its declarative sets.
func normalizeSurface(surface Surface) (Surface, error) {
	if !validName(surface.ID) || !validIdentity(surface.Version) || surface.MaxTotalBytes <= 0 || len(surface.Slots) == 0 {
		return Surface{}, fmt.Errorf("candidate: surface identity, slots and positive total limit required")
	}
	surface.Slots = slices.Clone(surface.Slots)
	seen := map[string]bool{}
	bindings := map[string]string{}
	for i := range surface.Slots {
		slot := &surface.Slots[i]
		if !validName(slot.Name) || seen[slot.Name] || slot.MaxBytes <= 0 || slot.MaxComponents <= 0 {
			return Surface{}, fmt.Errorf("candidate: invalid or duplicate slot %q or limits", slot.Name)
		}
		seen[slot.Name] = true
		normalized, err := normalizeSlot(*slot)
		if err != nil {
			return Surface{}, err
		}
		*slot = normalized

		if version, ok := bindings[slot.Binding.ID]; ok && version != slot.Binding.Version {
			return Surface{}, fmt.Errorf("candidate: conflicting binding %q", slot.Binding.ID)
		}
		bindings[slot.Binding.ID] = slot.Binding.Version
	}
	slices.SortFunc(surface.Slots, func(a, b Slot) int {
		return strings.Compare(a.Name, b.Name)
	})
	return surface, nil
}

// normalizeSlot owns and validates one slot's declarative sets.
func normalizeSlot(slot Slot) (Slot, error) {
	switch slot.Activation {
	case AlwaysOn, OnDemand, ChildContext, Inline:
	default:
		return Slot{}, fmt.Errorf("candidate: unsupported activation %q", slot.Activation)
	}
	if !validIdentity(slot.Binding.ID) || !validIdentity(slot.Binding.Version) {
		return Slot{}, fmt.Errorf("candidate: slot %q requires binding identity and version", slot.Name)
	}

	slot.Operations = slices.Clone(slot.Operations)
	slices.Sort(slot.Operations)
	if len(slot.Operations) == 0 || len(slices.Compact(slices.Clone(slot.Operations))) != len(slot.Operations) {
		return Slot{}, fmt.Errorf("candidate: slot %q requires unique operations", slot.Name)
	}
	for _, operation := range slot.Operations {
		if operation != Put && operation != Delete {
			return Slot{}, fmt.Errorf("candidate: unsupported operation %q", operation)
		}
	}
	slot.MediaTypes = slices.Clone(slot.MediaTypes)
	slices.Sort(slot.MediaTypes)
	if err := validateMediaTypes(slot.MediaTypes); err != nil {
		return Slot{}, err
	}

	slot.Capabilities = slices.Clone(slot.Capabilities)
	for _, capability := range slot.Capabilities {
		if !utf8.ValidString(capability) {
			return Slot{}, fmt.Errorf("candidate: invalid capability text for %q", slot.Name)
		}
	}
	if len(slot.Capabilities) == 0 {
		slot.Capabilities = nil
	}
	slices.Sort(slot.Capabilities)
	return slot, nil
}

func validateMediaTypes(mediaTypes []string) error {
	if len(mediaTypes) == 0 || len(slices.Compact(slices.Clone(mediaTypes))) != len(mediaTypes) {
		return fmt.Errorf("candidate: unique media types required")
	}
	for _, media := range mediaTypes {
		parsed, params, err := mime.ParseMediaType(media)
		if err != nil || parsed != media || len(params) != 0 || !strings.Contains(media, "/") || strings.Contains(media, "*") {
			return fmt.Errorf("candidate: invalid media type %q", media)
		}
	}
	return nil
}

func cloneSurface(surface Surface) Surface {
	surface.Slots = slices.Clone(surface.Slots)
	for i := range surface.Slots {
		slot := &surface.Slots[i]
		slot.MediaTypes = slices.Clone(slot.MediaTypes)
		slot.Operations = slices.Clone(slot.Operations)
		slot.Capabilities = slices.Clone(slot.Capabilities)
	}
	return surface
}

func validIdentity(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != ""
}
