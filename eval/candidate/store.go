package candidate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Store is a durable, surface-scoped candidate store. One host-owned Store must
// own writes to a directory. Methods serialize goroutines; cross-process writer
// coordination and authorization of the directory remain host responsibilities.
// Validators must not call back into the Store.
type Store struct {
	mu         sync.Mutex
	dir        string
	surface    Surface
	baseline   *Frozen
	validators []Validator
}

type storedFrozen struct {
	Manifest   Manifest    `json:"manifest"`
	Components []Component `json:"components"`
}

type registration struct {
	Version        int     `json:"version"`
	Surface        Surface `json:"surface"`
	BaselineDigest string  `json:"baseline_digest"`
}

// Open registers an authorized byte baseline, or reopens an identical scope.
// Source directories are not accepted. The host supplies retained bytes and
// re-registers its validators on reopen; validator code is not serialized.
func Open(ctx context.Context, dir string, surface Surface, baseline []Component, validators ...Validator) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	surface, err := normalizeSurface(surface)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, surface: surface, validators: slices.Clone(validators)}
	for _, validator := range validators {
		if validator == nil {
			return nil, errors.New("candidate: nil host validator")
		}
	}
	if err := s.validate(ctx, baseline); err != nil {
		return nil, err
	}
	s.baseline, err = makeFrozen(surface, "", baseline)
	if err != nil {
		return nil, err
	}
	if err := s.initialize(); err != nil {
		return nil, err
	}
	return s, nil
}

// Discover returns owned surface and baseline metadata, including supported
// bindings, limits, capabilities, activation and authorized component dependencies.
func (s *Store) Discover() Discovery {
	return Discovery{Surface: cloneSurface(s.surface), BaselineDigest: s.baseline.Digest(), Baseline: s.baseline.Manifest().Components}
}

// Create starts a new draft from the registered baseline.
func (s *Store) Create(ctx context.Context) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.create(ctx, s.baseline.Components())
}

// Fork starts a new draft from a frozen snapshot in this scope.
func (s *Store) Fork(ctx context.Context, digest string) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Draft{}, err
	}
	f, err := s.load(digest)
	if err != nil {
		return Draft{}, err
	}
	return s.create(ctx, f.Components())
}

// Draft returns an owned editable snapshot.
func (s *Store) Draft(ctx context.Context, id string) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Draft{}, err
	}
	return s.draft(id)
}

// Edit applies a validated batch atomically at the expected revision. Host
// validation failure leaves the draft unchanged, allowing the caller to repair it.
func (s *Store) Edit(ctx context.Context, id string, expected uint64, edits []Edit) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.current(ctx, id, expected)
	if err != nil {
		return Draft{}, err
	}
	if d.FrozenDigest != "" {
		return Draft{}, ErrFrozen
	}
	if len(edits) == 0 {
		return Draft{}, errors.New("candidate: empty edit batch")
	}
	components, err := s.apply(d.Components, edits)
	if err != nil {
		return Draft{}, err
	}
	if err := s.validate(ctx, components); err != nil {
		return Draft{}, err
	}
	if d.Revision == ^uint64(0) {
		return Draft{}, errors.New("candidate: revision exhausted")
	}
	d.Revision++
	d.Components = components
	if err := s.write("draft-"+id+".json", d); err != nil {
		return Draft{}, err
	}
	return d, nil
}

// Validate checks the current revision using core and host validators.
func (s *Store) Validate(ctx context.Context, id string, expected uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.current(ctx, id, expected)
	if err != nil {
		return err
	}
	return s.validate(ctx, d.Components)
}

// Freeze validates and retains the current revision as immutable content.
// Repeating a successful freeze at the same revision returns the same identity.
func (s *Store) Freeze(ctx context.Context, id string, expected uint64) (*Frozen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.current(ctx, id, expected)
	if err != nil {
		return nil, err
	}
	if d.FrozenDigest != "" {
		return s.load(d.FrozenDigest)
	}
	if err := s.validate(ctx, d.Components); err != nil {
		return nil, err
	}
	f, err := makeFrozen(s.surface, s.baseline.Digest(), d.Components)
	if err != nil {
		return nil, err
	}
	if err := s.saveFrozen(f); err != nil {
		return nil, err
	}
	d.FrozenDigest = f.Digest()
	if err := s.write("draft-"+id+".json", d); err != nil {
		return nil, err
	}
	return f, nil
}

// Load verifies and returns an immutable snapshot in this scope. Digest and
// content corruption fail explicitly; absent evidence is never empty content.
func (s *Store) Load(ctx context.Context, digest string) (*Frozen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.load(digest)
}

func randomID() string {
	return hashBytes([]byte(rand.Text()))[:32]
}

func validHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// applyEdit checks the affected slot permissions before changing the batch snapshot.
func applyEdit(components map[string]Component, slots map[string]Slot, name string, edit Edit) error {
	old, exists := components[name]
	switch edit.Operation {
	case Put:
		slot, ok := slots[edit.Component.Slot]
		if !ok || !slices.Contains(slot.Operations, Put) || (exists && !slices.Contains(slots[old.Slot].Operations, Put)) {
			return fmt.Errorf("candidate: put unsupported for %q", name)
		}
		components[name] = cloneComponents([]Component{edit.Component})[0]
	case Delete:
		if !exists {
			return fmt.Errorf("%w: component %q", ErrNotFound, name)
		}
		if !slices.Contains(slots[old.Slot].Operations, Delete) {
			return fmt.Errorf("candidate: delete unsupported for %q", name)
		}
		delete(components, name)
	default:
		return fmt.Errorf("candidate: unsupported operation %q", edit.Operation)
	}
	return nil
}

// initialize publishes or validates one immutable registered scope.
func (s *Store) initialize() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate: store must be a regular directory")
	}
	want := registration{Version: 1, Surface: s.surface, BaselineDigest: s.baseline.Digest()}
	var found registration
	err = s.read("registration.json", &found)
	if err == nil {
		if !reflect.DeepEqual(want, found) {
			return errors.New("candidate: registered surface or baseline differs")
		}
		if _, err := s.load(s.baseline.Digest()); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := s.saveFrozen(s.baseline); err != nil {
		return err
	}
	if err := s.write("registration.json", want); err != nil {
		return err
	}
	return nil
}

// apply resolves a whole edit batch before checking dependency closure.
func (s *Store) apply(original []Component, edits []Edit) ([]Component, error) {
	components := map[string]Component{}
	for _, c := range original {
		components[c.Name] = c
	}
	slots := map[string]Slot{}
	for _, slot := range s.surface.Slots {
		slots[slot.Name] = slot
	}
	changed := map[string]bool{}
	for _, edit := range edits {
		name := edit.Name
		if edit.Operation == Put {
			if name != "" && name != edit.Component.Name {
				return nil, errors.New("candidate: put name differs from component name")
			}
			name = edit.Component.Name
		}
		if !validName(name) || changed[name] {
			return nil, fmt.Errorf("candidate: invalid or duplicate edit name %q", name)
		}
		changed[name] = true
		if err := applyEdit(components, slots, name, edit); err != nil {
			return nil, err
		}
	}
	result := make([]Component, 0, len(components))
	for _, c := range components {
		result = append(result, c)
	}
	slices.SortFunc(result, func(a, b Component) int {
		return strings.Compare(a.Name, b.Name)
	})
	return result, nil
}

func (s *Store) validate(ctx context.Context, components []Component) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateComponents(s.surface, components); err != nil {
		return err
	}
	for _, validator := range s.validators {
		if err := validator(ctx, cloneComponents(components)); err != nil {
			return fmt.Errorf("candidate: host validation: %w", err)
		}
	}
	return ctx.Err()
}

func (s *Store) create(ctx context.Context, components []Component) (Draft, error) {
	if err := s.validate(ctx, components); err != nil {
		return Draft{}, err
	}
	id := randomID()
	d := Draft{ID: id, Revision: 1, Components: components}
	if err := s.write("draft-"+id+".json", d); err != nil {
		return Draft{}, err
	}
	return d, nil
}

func (s *Store) current(ctx context.Context, id string, expected uint64) (Draft, error) {
	if err := ctx.Err(); err != nil {
		return Draft{}, err
	}
	d, err := s.draft(id)
	if err != nil {
		return Draft{}, err
	}
	if d.Revision != expected {
		return Draft{}, ErrRevision
	}
	return d, nil
}

func (s *Store) draft(id string) (Draft, error) {
	if !validHex(id, 32) {
		return Draft{}, fmt.Errorf("%w: draft %q", ErrNotFound, id)
	}
	var d Draft
	if err := s.read("draft-"+id+".json", &d); err != nil {
		return Draft{}, err
	}
	if d.ID != id || d.Revision == 0 || (d.FrozenDigest != "" && !validHex(d.FrozenDigest, 64)) {
		return Draft{}, errors.New("candidate: corrupt draft identity")
	}
	if err := validateComponents(s.surface, d.Components); err != nil {
		return Draft{}, err
	}
	if d.FrozenDigest != "" {
		f, err := makeFrozen(s.surface, s.baseline.Digest(), d.Components)
		if err != nil {
			return Draft{}, err
		}
		if f.Digest() != d.FrozenDigest {
			return Draft{}, errors.New("candidate: frozen draft digest mismatch")
		}
		if _, err := s.load(d.FrozenDigest); err != nil {
			return Draft{}, err
		}
	}
	return d, nil
}

func (s *Store) load(digest string) (*Frozen, error) {
	if !validHex(digest, 64) {
		return nil, fmt.Errorf("%w: snapshot %q", ErrNotFound, digest)
	}
	var stored storedFrozen
	if err := s.read("frozen-"+digest+".json", &stored); err != nil {
		return nil, err
	}
	baseline := stored.Manifest.BaselineDigest
	if baseline != s.baseline.Digest() && !(baseline == "" && digest == s.baseline.Digest()) {
		return nil, errors.New("candidate: snapshot belongs to another baseline")
	}
	if err := validateComponents(s.surface, stored.Components); err != nil {
		return nil, err
	}
	f, err := makeFrozen(s.surface, baseline, stored.Components)
	if err != nil {
		return nil, err
	}
	if f.Digest() != digest || !reflect.DeepEqual(f.manifest, stored.Manifest) {
		return nil, errors.New("candidate: snapshot digest or manifest mismatch")
	}
	return f, nil
}

func (s *Store) saveFrozen(f *Frozen) error {
	name := "frozen-" + f.Digest() + ".json"
	var existing storedFrozen
	if err := s.read(name, &existing); err == nil {
		_, err := s.load(f.Digest())
		return err
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return s.write(name, storedFrozen{Manifest: f.Manifest(), Components: f.Components()})
}
