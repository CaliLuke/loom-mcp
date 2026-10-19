package eval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedConcurrentIsolation(t *testing.T) {
	root := t.TempDir()
	for _, variant := range []string{"first", "second"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, variant), []byte(variant+" frozen"), 0600))
	}
	ready := make(chan struct{}, 2)
	resume := make(chan struct{})
	var released atomic.Int32
	cfg := preparedTestConfig(func(_ context.Context, info PreparedAttemptInfo) (*PreparedAttempt, error) {
		sourceRoot, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		content, readErr := sourceRoot.ReadFile(info.Variant.ID)
		err = errors.Join(readErr, sourceRoot.Close())
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp(root, "lease-")
		if err != nil {
			return nil, err
		}
		var order []string
		lease := preparedTestLease(&order)
		lease.Inputs = []agenttest.Input{agenttest.User(string(content))}
		lease.Receipt.InputSnapshot = string(content)
		lease.Receipt.Environment.SnapshotID = dir
		lease.Collect = func(_ context.Context, input CollectionInput) ([]CollectedArtifact, error) {
			if input.EvidenceError != nil {
				return nil, input.EvidenceError
			}
			if input.Evidence == nil || len(input.Evidence.Records) != 1 {
				return nil, fmt.Errorf("missing capture for %s", info.Variant.ID)
			}
			call := input.Evidence.Records[0].Runs[0].Calls[0]
			part, ok := call.Request.Messages[0].Parts[0].(model.TextPart)
			if !ok || part.Text != string(content) {
				return nil, fmt.Errorf("candidate %s input changed", info.Variant.ID)
			}
			return []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain", Data: []byte(part.Text)}}, nil
		}
		lease.Release = func(context.Context) error {
			released.Add(1)
			return os.RemoveAll(dir)
		}
		ready <- struct{}{}
		<-resume
		return lease, nil
	})
	cfg.Variants = []Variant{{ID: "first"}, {ID: "second"}}
	cfg.Concurrency = 2
	store, writer := beginPreparedTest(t, &cfg)
	type result struct {
		report *Report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := Run(t.Context(), cfg)
		done <- result{r, err}
	}()
	for range 2 {
		<-ready
	}
	for _, variant := range []string{"first", "second"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, variant), []byte("mutated live source"), 0600))
	}
	close(resume)
	out := <-done
	require.NoError(t, out.err)
	assert.Equal(t, int32(2), released.Load())
	require.NoError(t, writer.Finish(out.report, out.err))
	for _, attempt := range out.report.Attempts {
		require.NoError(t, attempt.HarnessErr)
		assert.Equal(t, VerdictPassed, attempt.Verdict())
		data, err := store.ReadArtifact(t.Context(), writer.ID(), attempt.AttemptKey, "answer.txt")
		require.NoError(t, err)
		assert.Equal(t, attempt.Variant+" frozen", string(data))
		_, err = os.Stat(attempt.Prepared.Environment.SnapshotID)
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestPreparedObserverPersistenceAndPanic(t *testing.T) {
	var order []string
	cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		return preparedTestLease(&order), nil
	})
	cfg.Repeats = 2
	store, writer := beginPreparedTest(t, &cfg)
	calls := 0
	cfg.OnAttempt = func(a *AttemptRecord) {
		calls++
		loaded, err := store.Load(writer.ID())
		if assert.NoError(t, err) {
			assert.Len(t, loaded.Attempts, 1, "runner publishes before observer")
		}
		panic("observer broken")
	}
	report, err := Run(t.Context(), cfg)
	require.ErrorContains(t, err, "observer broken")
	assert.Equal(t, StopCallbackPanic, report.Stop)
	assert.Equal(t, 1, calls)
	require.NoError(t, writer.Finish(report, err))
	loaded, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.Contains(t, loaded.Attempts[0].HarnessError, "observer broken")
	assert.Equal(t, VerdictNotRun, loaded.Attempts[1].Verdict)
}
