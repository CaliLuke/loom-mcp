package eval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/google/uuid"
)

// Experiment is a loaded summary and its attempts in Index order.
type Experiment struct {
	Dir           string
	Header        ExperimentHeader
	Attempts      []*StoredAttempt
	TruncatedTail bool
}

// ErrNothingToRerun means no failed, error or not-run groups exist.
var ErrNothingToRerun = errors.New("eval: nothing to rerun")

// List returns summaries newest first by (Started, ID), ignoring reserved/temp paths.
func (s *Store) List() ([]ExperimentHeader, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var headers []ExperimentHeader
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := uuid.Parse(entry.Name())
		if err != nil || id.Version() != 7 || id.String() != entry.Name() {
			continue
		}
		h, err := s.readHeader(entry.Name())
		if err != nil {
			return nil, err
		}
		headers = append(headers, *h)
	}
	slices.SortFunc(headers, func(a, b ExperimentHeader) int {
		if order := b.Started.Compare(a.Started); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	return headers, nil
}

// Resolve accepts a full ID, a unique prefix of at least eight bytes, or last.
// Last means the newest experiment whose status is not running.
func (s *Store) Resolve(ref string) (string, error) {
	headers, err := s.List()
	if err != nil {
		return "", err
	}
	if ref != "last" && len(ref) < 8 {
		return "", fmt.Errorf("eval: experiment prefix must contain at least 8 characters")
	}
	found := ""
	for _, h := range headers {
		if ref == "last" {
			if h.Status != statusRunning {
				return h.ID, nil
			}
		} else if strings.HasPrefix(h.ID, ref) {
			if found != "" {
				return "", fmt.Errorf("eval: ambiguous experiment %q", ref)
			}
			found = h.ID
		}
	}
	if found == "" {
		return "", fmt.Errorf("eval: unknown experiment %q", ref)
	}
	return found, nil
}

// Last finds the newest non-running experiment sharing a pair, excluding one ID.
// It returns nil, nil when no experiment overlaps.
func (s *Store) Last(pairs [][2]string, exclude string) (*ExperimentHeader, error) {
	headers, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, h := range headers {
		if h.Status == statusRunning || h.ID == exclude {
			continue
		}
		for _, scenario := range h.Scenarios {
			for _, pair := range pairs {
				if scenario.ID == pair[0] && slices.Contains(scenario.Items, pair[1]) {
					return &h, nil
				}
			}
		}
	}
	return nil, nil
}

// Load strictly reads a canonical ID, retaining complete lines before a torn tail.
func (s *Store) Load(id string) (*Experiment, error) {
	h, err := s.readHeader(id)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, id)
	path := filepath.Join(dir, "attempts.jsonl")
	f, err := os.OpenInRoot(s.dir, filepath.Join(id, "attempts.jsonl"))
	if err != nil {
		return nil, err
	}
	attempts, truncated, readErr := readAttempts(f, path, id)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, err
	}
	return &Experiment{Dir: dir, Header: *h, Attempts: attempts, TruncatedTail: truncated}, nil
}

// Complete reports whether a terminal experiment contains all planned attempts.
// A stopped experiment can be complete when every not-run key is recorded.
func (e *Experiment) Complete() bool {
	return (e.Header.Status == statusComplete || e.Header.Status == statusStopped) && !e.TruncatedTail && len(e.Attempts) == e.Header.Planned
}

// Find selects #index, a full attempt key, or an unambiguous scenario:item:repeat.
func (e *Experiment) Find(ref string) (*StoredAttempt, error) {
	var found *StoredAttempt
	for _, a := range e.Attempts {
		short := fmt.Sprintf("%s:%s:%d", a.Scenario, a.Item, a.Repeat)
		if ref != "#"+strconv.Itoa(a.Index) && ref != a.AttemptKey.String() && ref != short {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("eval: ambiguous attempt %q", ref)
		}
		found = a
	}
	if found == nil {
		return nil, fmt.Errorf("eval: unknown attempt %q", ref)
	}
	return found, nil
}

// RerunSelection selects failed, error and not-run groups, including all repeats.
// Selection combines variants and qualified items as a cross-product superset.
func (e *Experiment) RerunSelection() (Selection, error) {
	var selection Selection
	for _, a := range e.Attempts {
		switch a.Verdict {
		case VerdictFailed, VerdictError, VerdictNotRun:
			selection.Variants = append(selection.Variants, a.Variant)
			selection.Items = append(selection.Items, a.Scenario+"/"+a.Item)
		case VerdictPassed, VerdictPending:
		}
	}
	if len(selection.Items) == 0 {
		return Selection{}, ErrNothingToRerun
	}
	selection.Variants = sortedUnique(selection.Variants)
	selection.Items = sortedUnique(selection.Items)
	return selection, nil
}

// Record restores and verifies the embedded export without a harness or store.
func (r StoredRun) Record() (*runrecord.RunRecord, error) {
	return runrecord.FromExport(r.Export)
}

// Trajectory reconstructs the exact root trajectory and its child trajectories.
func (r StoredRun) Trajectory() (*trajectory.Trajectory, error) {
	rec, err := r.Record()
	if err != nil {
		return nil, err
	}
	tr, err := trajectory.Build(rec, r.RunID, trajectory.Options{Output: &api.RunOutput{Final: r.Final}, EventsUnverified: r.EventsUnverified})
	if err != nil {
		return nil, err
	}
	tr.TraceID = r.TraceID
	return tr, nil
}

// Session restores trajectories in the original session run order.
func (a *StoredAttempt) Session() (trajectory.Session, error) {
	result := make(trajectory.Session, 0, len(a.Runs))
	for _, run := range a.Runs {
		tr, err := run.Trajectory()
		if err != nil {
			return nil, err
		}
		result = append(result, tr)
	}
	return result, nil
}
