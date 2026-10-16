package agenttest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/CaliLuke/loom-mcp/v2/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

// EvidenceSnapshot contains raw canonical records from a private local capture.
// Unlinked preserves call runs without a committed link in any root tree.
// These records are replay evidence, not filtered artifacts for sharing.
type EvidenceSnapshot = evidence.Snapshot

// WithEvidenceDir persists canonical evidence in a fresh harness-owned capture.
// The directory can exist, but its evidence database must not already exist.
// Stores and engine ownership remain with the harness. Capture errors fail runs.
func WithEvidenceDir(dir string) Option {
	return func(options *harnessOptions) error {
		if dir == "" || options.evidenceDir != "" {
			return errors.New("agenttest: exactly one nonempty evidence directory is required")
		}
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		options.evidenceDir = absolute
		return nil
	}
}

// ReadEvidence reopens a private capture without requiring a completed run or eval.
// Canonical adoption determines incomplete/abandoned classes; no success is inferred.
// It returns any readable records alongside explicit reconstruction errors.
func ReadEvidence(ctx context.Context, dir string) (*EvidenceSnapshot, error) {
	return evidence.Read(ctx, dir)
}

// EvidenceDir returns the selected private capture directory, or empty by default.
func (h *Harness) EvidenceDir() string {
	return h.evidenceDir
}

func (h *Harness) configureEvidence(ctx context.Context, dir string, options *runtime.Options) error {
	if dir == "" {
		return nil
	}
	store, err := evidence.Create(ctx, dir)
	if err != nil {
		return fmt.Errorf("agenttest: create evidence: %w", err)
	}
	h.evidence, h.evidenceDir = store, dir
	options.RunStore, options.RunEventStore = store, store
	return nil
}

func (h *Harness) closeEvidenceOnFailure(result **Harness, err *error) {
	if *result == nil && h.evidence != nil {
		*err = errors.Join(*err, h.evidence.Close())
	}
}

func (h *Harness) closeEvidence() error {
	if h.evidence == nil {
		return nil
	}
	h.evidenceReaders.Wait()
	return h.evidence.Close()
}

// beginEvidenceRead acquires ownership before Close can release the local store.
func (h *Harness) beginEvidenceRead() error {
	if h.evidence == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("agenttest: harness is closed")
	}
	h.evidenceReaders.Add(1)
	return nil
}

func (h *Harness) endEvidenceRead() {
	if h.evidence != nil {
		h.evidenceReaders.Done()
	}
}
