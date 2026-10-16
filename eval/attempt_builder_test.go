package eval

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttemptBuilderCapturesBeforeRegistration(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	cfg.Scenarios[0].Agent = "report"
	cfg.Harness = nil
	var writer *Writer
	var configured, registered, prepared bool
	cfg.AttemptHarness = NewAttemptHarnessFunc(func(_ context.Context, info AttemptInfo) (AttemptHarnessConfig, error) {
		configured = true
		assert.Equal(t, writer.keys[0], info.Key)
		assert.Equal(t, info.Key.Item, info.Item.ID)
		return AttemptHarnessConfig{
			Register: func(ctx context.Context, rt *runtime.Runtime) error {
				registered = true
				rows, readErr := store.ReadEvidence(ctx, writer.ID())
				if readErr != nil {
					return readErr
				}
				if !assert.Len(t, rows, 1) {
					return errors.New("capture absent during registration")
				}
				assert.Empty(t, rows[0].Error)
				assert.NotNil(t, rows[0].Snapshot)
				return registerReportTools(ctx, rt)
			},
			Options: []agenttest.Option{agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("done")))},
			Setup: func(h *agenttest.Harness) error {
				prepared = true
				assert.NotEmpty(t, h.EvidenceDir())
				return nil
			},
		}, nil
	})
	writer, err = store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	cfg.AttemptHarness = writer.CaptureHarness(cfg.AttemptHarness)
	report, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	require.True(t, configured && registered && prepared)
	require.Len(t, report.Attempts, 1)
	require.NoError(t, report.Attempts[0].HarnessErr)
	require.NoError(t, report.Attempts[0].Outcome.Err)
	rows, err := store.ReadEvidence(t.Context(), writer.ID())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, report.Attempts[0].AttemptKey, rows[0].Key)
	require.Len(t, rows[0].Snapshot.Records, 1)
	require.Len(t, rows[0].Snapshot.Records[0].Runs[0].Calls, 1)
	assert.Equal(t, runrecord.Adopted, rows[0].Snapshot.Records[0].Runs[0].Calls[0].Class)
}

func TestAttemptBuilderRejectsCaptureConflictBeforeRegistration(t *testing.T) {
	registered := false
	factory := NewAttemptHarnessFunc(func(context.Context, AttemptInfo) (AttemptHarnessConfig, error) {
		return AttemptHarnessConfig{Register: func(context.Context, *runtime.Runtime) error {
			registered = true
			return nil
		}, Options: []agenttest.Option{agenttest.WithEvidenceDir(t.TempDir())}}, nil
	})
	h, err := factory(t.Context(), AttemptInfo{}, agenttest.WithEvidenceDir(t.TempDir()))
	require.ErrorContains(t, err, "evidence")
	assert.Nil(t, h)
	assert.False(t, registered)
}

func TestAttemptBuilderConstructionErrors(t *testing.T) {
	for _, kind := range []string{"missing configure", "configure error", "missing registration"} {
		t.Run(kind, func(t *testing.T) {
			var configure func(context.Context, AttemptInfo) (AttemptHarnessConfig, error)
			if kind != "missing configure" {
				configure = func(context.Context, AttemptInfo) (AttemptHarnessConfig, error) {
					if kind == "configure error" {
						return AttemptHarnessConfig{}, assert.AnError
					}
					return AttemptHarnessConfig{}, nil
				}
			}
			h, err := NewAttemptHarnessFunc(configure)(t.Context(), AttemptInfo{})
			require.Error(t, err)
			assert.Nil(t, h)
			if kind == "configure error" {
				assert.ErrorIs(t, err, assert.AnError)
			}
		})
	}
}

func TestAttemptBuilderClosesFailedSetup(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			var constructed *agenttest.Harness
			factory := NewAttemptHarnessFunc(func(context.Context, AttemptInfo) (AttemptHarnessConfig, error) {
				return AttemptHarnessConfig{Register: registerReportTools, Setup: func(h *agenttest.Harness) error {
					constructed = h
					if panics {
						panic("setup failure")
					}
					return assert.AnError
				}}, nil
			})
			h, err := factory(t.Context(), AttemptInfo{})
			require.Error(t, err)
			assert.Nil(t, h)
			require.NotNil(t, constructed)
			_, runErr := constructed.Run(t.Context(), "report", nil)
			assert.ErrorContains(t, runErr, "closed")
		})
	}
}
