package calibrationprogress_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"example.com/longrun/internal/calibrationprogress"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgressReportsLongCellsAtBoundedCadence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output lockedBuffer
		reporter := calibrationprogress.Start(t.Context(), &output)
		reporter.Phase("candidates")
		reporter.Observe(gate.CalibrationProgress{CellsTotal: 2, SimulationsTotal: 6})
		initial := output.Len()
		reporter.Observe(gate.CalibrationProgress{CellsTotal: 2, SimulationsTotal: 6, SimulationsCompleted: 1})
		synctest.Wait()
		assert.Equal(t, initial, output.Len(), "individual simulations must not flood the log")
		time.Sleep(30 * time.Second)
		synctest.Wait()
		assert.Contains(t, output.String(), "cells=0/2")
		assert.Contains(t, output.String(), "simulations=1/6")
		assert.Contains(t, output.String(), "eta~2m30s")
		assert.Contains(t, output.String(), "elapsed=30s")
		reporter.Observe(gate.CalibrationProgress{CellsTotal: 2, CellsCompleted: 2, SimulationsTotal: 6, SimulationsCompleted: 6})
		reporter.Phase("verify evidence")
		assert.Contains(t, output.String(), "cells=2/2")
		assert.Contains(t, output.String(), "simulations=6/6")
		require.NoError(t, reporter.Close(nil))
		assert.Contains(t, output.String(), "status=complete")
		final := output.Len()
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, final, output.Len(), "reporter must stop after completion")
	})
}

func TestProgressUnknownETAAndFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output lockedBuffer
		reporter := calibrationprogress.Start(t.Context(), &output)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		assert.Contains(t, output.String(), "eta=unknown")
		failure := errors.New("evidence write failed")
		require.ErrorIs(t, reporter.Close(failure), failure)
		assert.Contains(t, output.String(), "status=failed")
		assert.NotContains(t, output.String(), "status=complete")
	})
}

type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, errors.New("progress sink unavailable") }

func TestProgressWriterFailureCancelsWork(t *testing.T) {
	reporter := calibrationprogress.Start(t.Context(), brokenWriter{})
	require.ErrorIs(t, reporter.Context().Err(), context.Canceled)
	err := reporter.Close(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "progress sink unavailable")
}

func TestProgressCancellationIsNotCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output lockedBuffer
		ctx, cancel := context.WithCancel(t.Context())
		reporter := calibrationprogress.Start(ctx, &output)
		cancel()
		require.ErrorIs(t, reporter.Close(nil), context.Canceled)
		assert.Contains(t, output.String(), "status=canceled")
		assert.NotContains(t, output.String(), "status=complete")
	})
}

// lockedBuffer permits snapshots while the reporter's heartbeat writes.
type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }
func (b *lockedBuffer) Len() int       { b.mu.Lock(); defer b.mu.Unlock(); return b.data.Len() }
