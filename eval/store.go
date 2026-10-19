package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Store reads and writes experiments in a caller-selected directory.
type Store struct {
	dir          string
	options      storeOptions
	now          func() time.Time
	newID        func() (uuid.UUID, error)
	beforeRename func() error
}

// StoreOption configures record size and write-error reporting.
type StoreOption func(*storeOptions)
type storeOptions struct {
	maxRecordBytes int
	onError        func(error)
}

// ExperimentMeta supplies human labels, lineage and optional injected provenance.
type ExperimentMeta struct {
	Label, Parent string
	Identity      *Identity
}

// WithMaxRecordBytes sets the export-omission threshold; n must be positive.
func WithMaxRecordBytes(n int) StoreOption {
	return func(o *storeOptions) {
		o.maxRecordBytes = n
	}
}

// WithOnError receives the first sticky write error for each writer.
func WithOnError(fn func(error)) StoreOption {
	return func(o *storeOptions) {
		o.onError = fn
	}
}

// Open creates a self-gitignored store without deleting existing data.
// New directories use mode 0700. Callers own the permissions of existing directories.
func Open(dir string, opts ...StoreOption) (*Store, error) {
	o := storeOptions{maxRecordBytes: 128 << 20}
	for _, opt := range opts {
		opt(&o)
	}
	if o.maxRecordBytes <= 0 {
		return nil, fmt.Errorf("eval: MaxRecordBytes must be positive")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	if err := initializeStoreDir(absolute); err != nil {
		return nil, err
	}
	return &Store{dir: absolute, options: o, now: time.Now, newID: uuid.NewV7}, nil
}

// DefaultStoreDir finds .loom-evals beside the nearest ancestor go.mod.
func DefaultStoreDir(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, ".loom-evals"), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("eval: no go.mod above %q", start)
		}
		dir = parent
	}
}

// Begin stages both headers privately, then atomically publishes the experiment directory.
func (s *Store) Begin(ctx context.Context, cfg Config, meta ExperimentMeta) (*Writer, error) {
	keys, err := Plan(cfg)
	if err != nil {
		return nil, err
	}
	return s.begin(ctx, newExperimentHeader(cfg, keys, "", meta, Identity{}, time.Time{}), keys, meta)
}

func (s *Store) begin(ctx context.Context, h ExperimentHeader, keys []AttemptKey, meta ExperimentMeta) (*Writer, error) {
	if !utf8.ValidString(meta.Label) || len(meta.Label) > 256 {
		return nil, fmt.Errorf("eval: label must be valid UTF-8 and at most 256 bytes")
	}
	if meta.Parent != "" {
		if _, err := s.readHeader(meta.Parent); err != nil {
			return nil, fmt.Errorf("eval: parent: %w", err)
		}
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	identity := meta.Identity
	if identity == nil {
		detected := DetectIdentity(ctx, ".")
		identity = &detected
	}
	h.ID, h.Started, h.Source, h.Versions = id.String(), s.now(), identity.Source, identity.Versions
	staging, err := s.stageExperiment(h)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, id.String())
	if err := s.publishDirectory(staging, dir); err != nil {
		return nil, err
	}
	return &Writer{store: s, dir: dir, header: h, keys: keys, written: make(map[int]Fingerprint)}, nil
}

func newExperimentHeader(cfg Config, keys []AttemptKey, id string, meta ExperimentMeta, identity Identity, now time.Time) ExperimentHeader {
	// Plan already validated the configuration. Reuse report provenance collection.
	plan := make([]plannedAttempt, len(keys))
	for i, key := range keys {
		plan[i].scenario.ID = key.Scenario
		plan[i].item.ID = key.Item
	}
	report := newReport(cfg, plan)
	variants := make([]StoredVariant, len(report.Variants))
	for i, v := range report.Variants {
		variants[i] = StoredVariant{ID: v.ID, Labels: v.Labels, Policy: v.Policy}
	}
	h := ExperimentHeader{Format: experimentFormat, Version: 1, ID: id, Label: meta.Label, Parent: meta.Parent, Status: statusRunning, Started: now, Source: identity.Source, Versions: identity.Versions, Config: StoredConfig{Repeats: report.Repeats, Concurrency: report.Concurrency, MaxTokens: report.MaxTokens, Selection: report.Selection}, Scenarios: report.Scenarios, Variants: variants, Planned: len(keys)}
	if cfg.PrepareAttempt != nil {
		h.Version, h.ExecutionKind = 2, ExecutionEvaluation
		h.Config.PrepareTimeout, h.Config.CleanupTimeout = cfg.PrepareTimeout, cfg.CleanupTimeout
	}
	return h
}

func (s *Store) readHeader(id string) (*ExperimentHeader, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 || parsed.String() != id {
		return nil, fmt.Errorf("eval: invalid experiment ID %q", id)
	}
	path := filepath.Join(s.dir, id, "experiment.json")
	f, err := os.OpenInRoot(s.dir, filepath.Join(id, "experiment.json"))
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(f)
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return nil, err
	}
	h, err := decodeExperiment(data, id)
	if err != nil {
		return nil, &DecodeError{Source: path, Line: 1, Err: err}
	}
	return h, nil
}
