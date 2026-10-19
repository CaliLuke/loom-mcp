package eval

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lifecycleProbeConfig(prepare PrepareAttemptFunc) ProbeConfig {
	return ProbeConfig{Key: AttemptKey{Variant: "base", Scenario: "task", Item: "one", Repeat: 1}, Item: Item{ID: "one", Input: "hello"}, Variant: Variant{ID: "base"}, Agent: "report", PrepareAttempt: prepare, PrepareTimeout: time.Second, CleanupTimeout: time.Second}
}

func TestProbeLifecycleStatus(t *testing.T) {
	for _, phase := range []string{"success", "missing", "quiesce", "collect", "release", "canceled", "before start"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var order []string
			lease := preparedTestLease(&order)
			switch phase {
			case "missing":
				lease.Collect = func(context.Context, CollectionInput) ([]CollectedArtifact, error) {
					return nil, nil
				}
			case "quiesce":
				lease.Quiesce = func(context.Context) error {
					return assert.AnError
				}
			case "collect":
				lease.Collect = func(context.Context, CollectionInput) ([]CollectedArtifact, error) {
					return nil, assert.AnError
				}
			case "release":
				lease.Release = func(context.Context) error {
					return assert.AnError
				}
			case "canceled":
				lease.Harness.Setup = func(_ *agenttest.Harness) error {
					cancel()
					return context.Canceled
				}
			}
			cfg := lifecycleProbeConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
				return lease, nil
			})
			store, err := Open(t.TempDir())
			require.NoError(t, err)
			writer, err := store.BeginProbe(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			cfg.PreparedWriter = writer
			if phase == "before start" {
				cancel()
			}
			report, err := Probe(ctx, cfg)
			if phase == "canceled" || phase == "before start" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, report)
			r := report.Attempts[0]
			assert.Equal(t, "not_assessed", r.Probe.Quality)
			if r.Outcome != nil {
				assert.Empty(t, r.Outcome.Checks)
				assert.Empty(t, r.Outcome.Claims)
				assert.Empty(t, r.Outcome.Judgments)
			}
			switch phase {
			case "before start":
				assert.Equal(t, "not_run", r.Probe.Execution)
				assert.Equal(t, VerdictNotRun, r.Verdict())
				assert.Empty(t, order)
			case "canceled":
				assert.Equal(t, "canceled", r.Probe.Execution)
				assert.Equal(t, VerdictError, r.Verdict())
			case "success":
				assert.Equal(t, "succeeded", r.Probe.Execution)
				assert.Equal(t, "complete", r.Probe.Delivery)
				assert.Equal(t, "complete", r.Probe.Evidence)
				assert.Equal(t, VerdictUnassessed, r.Verdict())
			default:
				assert.Equal(t, "succeeded", r.Probe.Execution)
				assert.Equal(t, VerdictError, r.Verdict())
			}
			if phase == "quiesce" {
				assert.Equal(t, "provisional", r.Probe.Evidence)
			}
			if phase == "missing" || phase == "collect" {
				assert.Equal(t, "incomplete", r.Probe.Delivery)
				assert.Equal(t, "incomplete", r.Probe.Evidence)
			}
			require.NoError(t, writer.Finish(report, err))
			loaded, loadErr := store.Load(writer.ID())
			require.NoError(t, loadErr)
			assert.Equal(t, *r.Probe, *loaded.Attempts[0].Probe)
		})
	}
}

func TestProbeAdmission(t *testing.T) {
	base := lifecycleProbeConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		panic("invalid config must not acquire")
	})
	for _, change := range []func(*ProbeConfig){
		func(c *ProbeConfig) {
			c.Key = AttemptKey{}
		},
		func(c *ProbeConfig) {
			c.Item.ID = "other"
		},
		func(c *ProbeConfig) {
			c.Variant.ID = "other"
		},
		func(c *ProbeConfig) {
			c.Timeout = -time.Second
		},
		func(c *ProbeConfig) {
			c.PrepareTimeout = 0
		},
		func(c *ProbeConfig) {
			c.CleanupTimeout = 0
		},
		func(c *ProbeConfig) {
			c.PrepareAttempt = nil
		},
	} {
		cfg := base
		change(&cfg)
		_, err := Probe(t.Context(), cfg)
		require.Error(t, err)
	}
	_, err := Probe(t.Context(), base)
	assert.ErrorContains(t, err, "writer")
}
