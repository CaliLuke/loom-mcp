package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreFinishPersistsCallbackPanic(t *testing.T) {
	cfg := storeConfig()
	cfg.Repeats = 2
	cfg.Scenarios[0].Expect = []expect.Expectation{expect.MaxModelCalls(0)}
	cfg.Scenarios[0].Drive = func(context.Context, *agenttest.Harness, Item) (*agenttest.SessionResult, error) {
		return &agenttest.SessionResult{Runs: []*agenttest.Result{{RunID: "run", Record: &runrecord.RunRecord{RootRunID: "run"}}}}, nil
	}
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	cfg.OnAttempt = writer.OnAttempt(func(a *AttemptRecord) {
		assert.Equal(t, VerdictPassed, a.Verdict())
		// Preserve the CLI's existing durability-before-progress contract.
		current, loadErr := store.Load(writer.ID())
		require.NoError(t, loadErr)
		require.Len(t, current.Attempts, 1)
		assert.Equal(t, VerdictPassed, current.Attempts[0].Verdict)
		panic("progress failed")
	})
	report, runErr := Run(t.Context(), cfg)
	require.ErrorContains(t, runErr, "progress failed")
	require.ErrorContains(t, report.Attempts[0].HarnessErr, "progress failed")
	assert.Equal(t, VerdictError, report.Attempts[0].Verdict())
	assert.Equal(t, VerdictNotRun, report.Attempts[1].Verdict())
	require.NoError(t, writer.Finish(report, runErr))
	stored, err := store.Load(writer.ID())
	require.NoError(t, err)
	require.Len(t, stored.Attempts, 2)
	for i, a := range report.Attempts {
		want, err := storedAttempt(a)
		require.NoError(t, err)
		// Compare canonical persisted fields (including verdict and harness_error),
		// avoiding nil/empty differences introduced by JSON decoding.
		wantJSON, err := encodeStore(want)
		require.NoError(t, err)
		gotJSON, err := encodeStore(stored.Attempts[i])
		require.NoError(t, err)
		assert.JSONEq(t, string(wantJSON), string(gotJSON))
	}
}

func TestStoreFinishReplacementFailurePreservesLog(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	report := storeReport(t, cfg)
	require.NoError(t, writer.Append(report.Attempts[0]))
	path := filepath.Join(writer.dir, "attempts.jsonl")
	before, err := readStoreTestFile(path)
	require.NoError(t, err)
	report.Attempts[0].HarnessErr = errors.New("final failure")
	failure := errors.New("replacement failed")
	store.beforeRename = func() error {
		return failure
	}
	require.ErrorIs(t, writer.Finish(report, nil), failure)
	require.ErrorIs(t, writer.Err(), failure)
	after, err := readStoreTestFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	entries, err := os.ReadDir(writer.dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".attempts.jsonl.tmp-"))
	}
}
