package agentfeatures_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	selvedgeengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/selvedge"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/require"
)

func TestSelvedgeAcceptance(t *testing.T) {
	t.Run("generated coordinator matches in-memory behavior", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		executor := newRecordingWorkflowExecutor()
		// Each deterministic Now is persisted. Keep the paused interval longer
		// than the active budget without making this smoke a latency benchmark.
		runGeneratedFeatureAwaitScenario(t, ctx, fx, executor, "sess-selvedge-core", "run-selvedge-core", 35*time.Second, 30*time.Second, agentsruntime.WithRunTimeBudget(30*time.Second))
	})
}

func newSelvedgeAcceptanceEngine(t *testing.T, db string) *selvedgeengine.Engine {
	t.Helper()
	app := sv.NewApp(sv.WithDatabase(db), sv.WithMaxPollInterval(50*time.Millisecond), sv.WithActivityPollInterval(10*time.Millisecond), sv.WithWaitPollInterval(10*time.Millisecond), sv.WithTimerCheckInterval(10*time.Millisecond), sv.WithWorkflowResumptionInterval(10*time.Millisecond), sv.WithLeaderHeartbeatInterval(20*time.Millisecond), sv.WithLeaderLeaseDuration(100*time.Millisecond))
	eng, err := selvedgeengine.NewWorker(selvedgeengine.Options{App: app})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})

	return eng
}
