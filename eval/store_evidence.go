package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// AttemptEvidence binds private canonical evidence to a planned eval attempt.
// Error is explicit when capture is absent, corrupt, or not reconstructable.
// No quality verdict is inferred from lifecycle evidence.
type AttemptEvidence struct {
	Key      AttemptKey
	Index    int
	Snapshot *agenttest.EvidenceSnapshot
	Error    string
}

type evidencePlan struct {
	Version    int          `json:"version"`
	Experiment string       `json:"experiment"`
	Keys       []AttemptKey `json:"keys"`
}

type evidenceBinding struct {
	Version    int        `json:"version"`
	Experiment string     `json:"experiment"`
	Index      int        `json:"index"`
	Key        AttemptKey `json:"key"`
}

// CaptureHarness binds a planned attempt before factory entry and enables private
// durable evidence. The factory must forward opts to agenttest.NewHarness.
// Binding and ignored-option errors are sticky writer errors. Factory errors,
// including capture initialization errors, remain attempt-local and explicit.
func (w *Writer) CaptureHarness(factory AttemptHarnessFunc) AttemptHarnessFunc {
	return func(ctx context.Context, info AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		dir, err := w.bindEvidence(info.Key)
		if err != nil {
			return nil, err
		}
		if factory == nil {
			return nil, w.evidenceError(errors.New("eval: evidence requires a harness factory"))
		}
		h, err := factory(ctx, info, append(slices.Clone(opts), agenttest.WithEvidenceDir(filepath.Join(dir, "capture")))...)
		if err != nil || h == nil {
			return h, err
		}
		if h.EvidenceDir() != filepath.Join(dir, "capture") {
			return nil, w.evidenceError(errors.Join(errors.New("eval: factory did not enable planned evidence capture"), h.Close()))
		}
		return h, nil
	}
}

// ReadEvidence reads bound captures independently of completed attempt records.
// Missing or corrupt captures remain rows with Error and contribute to the error
// return. Without capture enabled, the returned slice is empty.
func (s *Store) ReadEvidence(ctx context.Context, experimentID string) ([]AttemptEvidence, error) {
	header, err := s.readHeader(experimentID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, experimentID, "evidence")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plan, err := readEvidencePlan(dir, header)
	if err != nil {
		return nil, err
	}
	var results []AttemptEvidence
	var problems []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result, readErr := readEvidenceEntry(ctx, dir, entry.Name(), plan)
		if readErr != nil {
			problems = append(problems, readErr)
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	slices.SortFunc(results, func(a, b AttemptEvidence) int {
		return a.Index - b.Index
	})
	return results, errors.Join(problems...)
}

// bindEvidence serializes manifest publication, never application factory work.
func (w *Writer) bindEvidence(key AttemptKey) (string, error) {
	w.mu.Lock()
	var err error
	var dir string
	switch {
	case w.err != nil:
		err = w.err
	case w.finished:
		err = errors.New("eval: writer is finished")
	default:
		index := slices.Index(w.keys, key)
		if index < 0 {
			err = errors.New("eval: evidence attempt does not match plan")
		} else {
			dir, err = w.writeEvidenceBinding(index, key)
		}
	}
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return dir, err
}

func (w *Writer) writeEvidenceBinding(index int, key AttemptKey) (string, error) {
	dir := filepath.Join(w.dir, "evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := syncDirectory(w.dir); err != nil {
		return "", err
	}
	if err := writeEvidenceJSON(dir, "plan.json", evidencePlan{Version: 1, Experiment: w.ID(), Keys: w.keys}); err != nil {
		return "", err
	}
	attemptDir := filepath.Join(dir, strconv.Itoa(index))
	if err := os.Mkdir(attemptDir, 0700); err != nil {
		return "", err
	}
	if err := syncDirectory(dir); err != nil {
		return "", err
	}
	if err := writeEvidenceJSON(attemptDir, "binding.json", evidenceBinding{Version: 1, Experiment: w.ID(), Index: index, Key: key}); err != nil {
		return "", err
	}
	return attemptDir, nil
}

func (w *Writer) evidenceError(err error) error {
	w.mu.Lock()
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return err
}
