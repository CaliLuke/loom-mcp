package candidate

import (
	"fmt"
	"reflect"
	"slices"
)

// editedOrigins removes only explicitly authored names after a successful edit.
func editedOrigins(origins []ComponentOrigin, edits []Edit) []ComponentOrigin {
	changed := make(map[string]bool, len(edits))
	for _, edit := range edits {
		name := edit.Name
		if edit.Operation == Put {
			name = edit.Component.Name
		}
		changed[name] = true
	}
	result := make([]ComponentOrigin, 0, len(origins))
	for _, origin := range origins {
		if !changed[origin.Name] {
			result = append(result, origin)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// componentRef canonically describes bytes and dependency metadata for comparison.
func componentRef(c Component) ComponentRef {
	deps := slices.Clone(c.Dependencies)
	if len(deps) == 0 {
		deps = nil
	}
	slices.Sort(deps)
	return ComponentRef{Name: c.Name, Slot: c.Slot, MediaType: c.MediaType, Digest: hashBytes(c.Data), Size: int64(len(c.Data)), Dependencies: deps}
}

// validateOriginStructure checks local origin integrity without reading ancestors.
func validateOriginStructure(components []Component, origins []ComponentOrigin, self string) error {
	byName := make(map[string]Component, len(components))
	for _, c := range components {
		byName[c.Name] = c
	}
	previous := ""
	for _, origin := range origins {
		c, ok := byName[origin.Name]
		if !ok || !validName(origin.Name) || origin.Name <= previous || !validHex(origin.SourceCandidate, 64) || !validHex(origin.SourceDigest, 64) || origin.SourceCandidate == self || origin.SourceDigest != hashBytes(c.Data) {
			return fmt.Errorf("candidate: invalid origin for %q", origin.Name)
		}
		previous = origin.Name
	}
	return nil
}

// validateOrigins compares each import with its immediate source only. Source
// reconstruction verifies its own hash and local origins without recursive I/O.
func (s *Store) validateOrigins(components []Component, origins []ComponentOrigin, self string) error {
	if err := validateOriginStructure(components, origins, self); err != nil {
		return err
	}
	byName := make(map[string]ComponentRef, len(components))
	for _, c := range components {
		byName[c.Name] = componentRef(c)
	}
	sources := make(map[string]map[string]ComponentRef)
	for _, origin := range origins {
		refs, ok := sources[origin.SourceCandidate]
		if !ok {
			source, err := s.loadContent(origin.SourceCandidate)
			if err != nil {
				return err
			}
			refs = make(map[string]ComponentRef, len(source.manifest.Components))
			for _, ref := range source.manifest.Components {
				refs[ref.Name] = ref
			}
			sources[origin.SourceCandidate] = refs
		}
		ref, ok := refs[origin.Name]
		if !ok || !reflect.DeepEqual(ref, byName[origin.Name]) {
			return fmt.Errorf("candidate: source component mismatch for %q", origin.Name)
		}
	}
	return nil
}
