package selvedge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConformance(t *testing.T) {
	enginetest.RunConformance(t, conformanceFactory(func(t *testing.T) string {
		return filepath.Join(t.TempDir(), "s.db")
	}))
}

func conformanceFactory(database func(*testing.T) string) enginetest.Factory {
	gaps := make(map[string]string)
	for _, kind := range []string{"hook", "planner", "tool", "finish", "record_content", "child_dispatch", "seal", "recall", "summary"} {
		gaps["activity_options/"+kind] = "per-call options and heartbeat metadata need S15a/S15b; removed by S17b"
	}
	return enginetest.Factory{Durable: true, Retries: false, KnownGaps: gaps, New: func(t *testing.T) enginetest.Fixture {
		db := database(t)
		client := testApp(t, db)
		require.NoError(t, client.Start(t.Context()))
		t.Cleanup(func() {
			assert.NoError(t, client.Shutdown(context.Background()))
		})
		newWorker := func() *Engine {
			e, err := NewWorker(Options{App: testApp(t, db), Client: client})
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, e.Close())
			})
			return e
		}
		current := newWorker()
		queue := "selvedge-conformance-" + uuid.NewString()
		return enginetest.Fixture{Engine: current, TaskQueue: queue, ActivityQueue: queue + "-activities", Restart: func(t *testing.T, register func(engine.Engine)) engine.Engine {
			require.NoError(t, current.Close())
			current = newWorker()
			register(current)
			require.NoError(t, current.SealRegistration(t.Context()))
			return current
		}, VerifyActivityOptions: func(*testing.T, string, string, []engine.ActivityOptions) error {
			return &enginetest.GapError{Err: errors.New("no recorded options before S15a")}
		}}
	}}
}
