package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeConfig() Config {
	return Config{Harness: NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
		return nil
	}, nil), Scenarios: []Scenario{{ID: "chat", Agent: "agent", Expect: []expect.Expectation{expect.Completed()}, Items: Items(Item{ID: "one", Input: "hello"})}}}
}
func storeReport(t *testing.T, cfg Config) *Report {
	t.Helper()
	plan, err := buildPlan(cfg)
	require.NoError(t, err)
	r := newReport(cfg, plan)
	r.Finished = time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	for _, a := range r.Attempts {
		a.Outcome = &Outcome{}
	}
	return r
}
func TestStoreLayoutAndHeaderGolden(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".loom-evals")
	s, err := Open(dir)
	require.NoError(t, err)
	fixed := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		return fixed
	}
	s.newID = func() (uuid.UUID, error) {
		return uuid.MustParse("0199d77a-0000-7000-8000-000000000001"), nil
	}
	identity := Identity{Source: VCS{System: "git", Revision: "abc", Origin: "buildinfo"}, Versions: Versions{Go: "go1.27.2", MainModule: "example.com/app", MainVersion: "v1", LoomMCP: "v2", Loom: "v1"}}
	w, err := s.Begin(t.Context(), storeConfig(), ExperimentMeta{Label: "baseline", Identity: &identity})
	require.NoError(t, err)
	id, err := uuid.Parse(w.ID())
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0700}, {filepath.Join(dir, w.ID()), 0700}, {filepath.Join(dir, ".gitignore"), 0600}, {filepath.Join(dir, w.ID(), "experiment.json"), 0600}, {filepath.Join(dir, w.ID(), "attempts.jsonl"), 0600}} {
		info, err := os.Stat(entry.path)
		require.NoError(t, err)
		assert.Equal(t, entry.mode, info.Mode().Perm())
	}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	ignored, err := root.ReadFile(".gitignore")
	require.NoError(t, root.Close())
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(ignored))
	data, err := readStoreTestFile(filepath.Join(dir, w.ID(), "experiment.json"))
	require.NoError(t, err)
	want, err := readStoreTestFile("testdata/store/experiment.golden.json")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(string(want)), string(data))
	h, err := decodeExperiment(data, w.ID())
	require.NoError(t, err)
	assert.Equal(t, identity.Source, h.Source)
	assert.Equal(t, identity.Versions, h.Versions)
	lines, err := readStoreTestFile(filepath.Join(dir, w.ID(), "attempts.jsonl"))
	require.NoError(t, err)
	_, tail, err := readAttempts(strings.NewReader(string(lines)), "attempts.jsonl", w.ID())
	require.NoError(t, err)
	assert.False(t, tail)
}

func TestStoreFinish(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stop   StopReason
		runErr error
		status string
	}{{"complete", "", nil, "complete"}, {"tokens", StopTokenLimit, nil, "stopped"}, {"cancel", StopCanceled, context.Canceled, "stopped"}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := storeConfig()
			cfg.Repeats = 2
			s, err := Open(t.TempDir())
			require.NoError(t, err)
			w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			r := storeReport(t, cfg)
			r.Stop = tc.stop
			r.Tokens = 7
			if tc.stop != "" {
				r.Attempts[1].Outcome = nil
				r.Attempts[1].NotRun = tc.stop
			}
			require.NoError(t, w.Append(r.Attempts[0]))
			require.NoError(t, w.Finish(r, tc.runErr))
			data, err := readStoreTestFile(filepath.Join(s.dir, w.ID(), "experiment.json"))
			require.NoError(t, err)
			h, err := decodeExperiment(data, w.ID())
			require.NoError(t, err)
			assert.Equal(t, tc.status, h.Status)
			assert.Equal(t, 7, h.Tokens)
			assert.Equal(t, r.Finished, h.Finished)
			assert.Equal(t, tc.stop, h.Stop)
			assert.Equal(t, errorText(tc.runErr), h.RunError)
			data, err = readStoreTestFile(filepath.Join(s.dir, w.ID(), "attempts.jsonl"))
			require.NoError(t, err)
			attempts, _, err := readAttempts(strings.NewReader(string(data)), "attempts.jsonl", w.ID())
			require.NoError(t, err)
			assert.Len(t, attempts, 2)
		})
	}
}

func TestStoreRejectDuplicateAndWrongPlan(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	r := storeReport(t, cfg)
	require.NoError(t, w.Append(r.Attempts[0]))
	require.Error(t, w.Append(r.Attempts[0]))
	r.Attempts[0].Item = "other"
	require.Error(t, w.Finish(r, nil))
}

func TestStoreAtomicHeader(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	path := filepath.Join(s.dir, w.ID(), "experiment.json")
	before, err := readStoreTestFile(path)
	require.NoError(t, err)
	s.beforeRename = func() error {
		return errors.New("injected rename failure")
	}
	require.ErrorContains(t, w.Finish(storeReport(t, cfg), nil), "injected")
	after, err := readStoreTestFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestStoreSizeLimit(t *testing.T) {
	s, err := Open(t.TempDir(), WithMaxRecordBytes(1024))
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	r := storeReport(t, cfg)
	r.Attempts[0].Outcome.Session = &agenttest.SessionResult{Runs: []*agenttest.Result{{RunID: "run", Record: &runrecord.RunRecord{RootRunID: "run", Content: map[runstore.Hash][]byte{"blob": []byte(strings.Repeat("x", 2000))}}}}}
	require.NoError(t, w.Finish(r, nil))
	data, err := readStoreTestFile(filepath.Join(s.dir, w.ID(), "attempts.jsonl"))
	require.NoError(t, err)
	attempts, _, err := readAttempts(strings.NewReader(string(data)), "attempts.jsonl", w.ID())
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.NotEmpty(t, attempts[0].StorageError)
	assert.Equal(t, "null", string(attempts[0].Runs[0].Export))
}

func TestStoreStickyWriteError(t *testing.T) {
	called, next := 0, 0
	s, err := Open(t.TempDir(), WithOnError(func(error) {
		called++
	}))
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	dir := filepath.Join(s.dir, w.ID())
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	require.NoError(t, root.Chmod(".", 0500))
	t.Cleanup(func() {
		assert.NoError(t, root.Chmod(".", 0700))
		assert.NoError(t, root.Close())
	})
	// Remove write access from the existing file as well: directory permissions alone
	// do not prohibit opening an existing O_APPEND file on POSIX.
	path := filepath.Join(dir, "attempts.jsonl")
	require.NoError(t, os.Chmod(path, 0400))
	r := storeReport(t, cfg)
	callback := w.OnAttempt(func(*AttemptRecord) {
		next++
	})
	require.NotPanics(t, func() {
		callback(r.Attempts[0])
		callback(r.Attempts[0])
	})
	require.Error(t, w.Err())
	assert.Equal(t, 1, called)
	assert.Equal(t, 2, next)
	require.Error(t, w.Finish(r, nil))
}

func TestIdentityBuildInfo(t *testing.T) {
	info := &debug.BuildInfo{GoVersion: "go1.27.2", Main: debug.Module{Path: "example.com/app", Version: "v3"}, Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}, Deps: []*debug.Module{{Path: "github.com/CaliLuke/loom-mcp/v2", Version: "v2.1"}, {Path: "github.com/CaliLuke/loom", Version: "v1.2"}}}
	got := identityFromBuildInfo(info)
	assert.Equal(t, VCS{System: "git", Revision: "abc", Modified: true, Origin: "buildinfo"}, got.Source)
	assert.Equal(t, Versions{Go: "go1.27.2", MainModule: "example.com/app", MainVersion: "v3", LoomMCP: "v2.1", Loom: "v1.2"}, got.Versions)
	assert.Equal(t, "none", identityFromBuildInfo(nil).Source.Origin)
}

func TestStoreDuplicateDoesNotPreventFinish(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	r := storeReport(t, cfg)
	require.NoError(t, w.Append(r.Attempts[0]))
	require.Error(t, w.Append(r.Attempts[0]))
	assert.NoError(t, w.Err())
	require.NoError(t, w.Finish(r, nil))
}

func readStoreTestFile(path string) ([]byte, error) {
	f, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(f)
	return data, errors.Join(readErr, f.Close())
}

// B1: Neither header-commit nor directory-publication failure can poison the store.
func TestStoreBeginFailureKeepsStoreReadable(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("rename_%d", failAt), func(t *testing.T) {
			s, err := Open(t.TempDir())
			require.NoError(t, err)
			cfg := storeConfig()
			baseline, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			require.NoError(t, baseline.Finish(storeReport(t, cfg), nil))
			candidate := uuid.MustParse("0199d77a-0000-7000-8000-000000000009")
			s.newID = func() (uuid.UUID, error) {
				return candidate, nil
			}
			renames := 0
			s.beforeRename = func() error {
				renames++
				if renames == failAt {
					return errors.New("injected Begin rename failure")
				}
				return nil
			}
			_, err = s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.ErrorContains(t, err, "injected Begin rename failure")
			assert.Equal(t, failAt, renames)
			_, err = os.Stat(filepath.Join(s.dir, candidate.String()))
			require.ErrorIs(t, err, os.ErrNotExist)
			entries, err := os.ReadDir(s.dir)
			require.NoError(t, err)
			staging := 0
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
					staging++
				}
			}
			assert.Equal(t, 1, staging)
			headers, err := s.List()
			require.NoError(t, err)
			require.Len(t, headers, 1)
			assert.Equal(t, baseline.ID(), headers[0].ID)
			resolved, err := s.Resolve("last")
			require.NoError(t, err)
			assert.Equal(t, baseline.ID(), resolved)
			last, err := s.Last([][2]string{{"chat", "one"}}, "")
			require.NoError(t, err)
			require.NotNil(t, last)
			assert.Equal(t, baseline.ID(), last.ID)
			s.beforeRename = nil
			later, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			assert.Equal(t, candidate.String(), later.ID())
			loaded, err := s.Load(later.ID())
			require.NoError(t, err)
			assert.Equal(t, statusRunning, loaded.Header.Status)
			headers, err = s.List()
			require.NoError(t, err)
			assert.Len(t, headers, 2)
		})
	}
}

func TestStoreOpenPreservesExistingDirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, root.Close())
	})
	require.NoError(t, root.Chmod(".", 0750))
	_, err = Open(dir)
	require.NoError(t, err)
	info, err := root.Stat(".")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0750), info.Mode().Perm())
}
