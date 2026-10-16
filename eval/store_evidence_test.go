package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func TestCaptureEvidenceWithoutCompletedAttempt(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	cfg.Harness = NewHarnessFunc(registerReportTools, nil, agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("done"))))
	cfg.Scenarios[0].Agent = "report"
	writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	factory := harnessFactory(cfg)
	cfg.Harness = nil
	cfg.AttemptHarness = writer.CaptureHarness(func(ctx context.Context, info AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		// The binding is durable before the factory can register or send a request.
		rows, err := store.ReadEvidence(ctx, writer.ID())
		require.Error(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, info.Key, rows[0].Key)
		assert.NotEmpty(t, rows[0].Error)
		return factory(ctx, info, opts...)
	})
	report, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, report.Attempts[0].HarnessErr)
	rows, err := store.ReadEvidence(t.Context(), writer.ID())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, report.Attempts[0].AttemptKey, rows[0].Key)
	require.Len(t, rows[0].Snapshot.Records, 1)
	assert.Equal(t, runrecord.Adopted, rows[0].Snapshot.Records[0].Runs[0].Calls[0].Class)
	loaded, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.Empty(t, loaded.Attempts, "capture does not require final attempt lines")
	assert.Equal(t, statusRunning, loaded.Header.Status)
}

func TestCaptureEvidenceRejectsMissingAndMismatchedCapture(t *testing.T) {
	for _, kind := range []string{"missing database", "corrupt binding"} {
		t.Run(kind, func(t *testing.T) {
			store, err := Open(t.TempDir())
			require.NoError(t, err)
			cfg := storeConfig()
			writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			factory := writer.CaptureHarness(harnessFactory(cfg))
			harness, err := factory(t.Context(), AttemptInfo{Key: writer.keys[0], Variant: Variant{ID: DefaultVariant}})
			require.NoError(t, err)
			require.NoError(t, harness.Close())
			dir := filepath.Join(writer.dir, "evidence", "0")
			if kind == "missing database" {
				require.NoError(t, os.Remove(filepath.Join(dir, "capture", "evidence.sqlite")))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "binding.json"), []byte(`{"version":1,"experiment":"wrong","index":0}`), 0600))
			}
			rows, err := store.ReadEvidence(t.Context(), writer.ID())
			require.Error(t, err)
			require.Len(t, rows, 1)
			assert.NotEmpty(t, rows[0].Error)
		})
	}
}

func TestCaptureEvidenceErrorsAreExplicit(t *testing.T) {
	for _, kind := range []string{"unknown attempt", "duplicate", "ignored options", "write failure"} {
		t.Run(kind, func(t *testing.T) {
			store, err := Open(t.TempDir())
			require.NoError(t, err)
			cfg := storeConfig()
			writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			info := AttemptInfo{Key: writer.keys[0], Variant: Variant{ID: DefaultVariant}}
			factory := harnessFactory(cfg)
			switch kind {
			case "unknown attempt":
				info.Key.Item = "unknown"
			case "duplicate":
				h, err := writer.CaptureHarness(factory)(t.Context(), info)
				require.NoError(t, err)
				require.NoError(t, h.Close())
			case "ignored options":
				factory = func(ctx context.Context, _ AttemptInfo, _ ...agenttest.Option) (*agenttest.Harness, error) {
					return agenttest.NewHarness(ctx, func(context.Context, *runtime.Runtime) error {
						return nil
					})
				}
			case "write failure":
				require.NoError(t, os.WriteFile(filepath.Join(writer.dir, "evidence"), []byte("not a directory"), 0600))
			}
			h, err := writer.CaptureHarness(factory)(t.Context(), info)
			require.Error(t, err)
			assert.Nil(t, h)
			require.Error(t, writer.Err())
		})
	}
}

func TestCaptureEvidenceInitializationFailureIsAttemptLocal(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	writer, err := store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	factory := writer.CaptureHarness(func(ctx context.Context, _ AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		captureDir := filepath.Join(writer.dir, "evidence", "0", "capture")
		if err := os.Mkdir(captureDir, 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(captureDir, "evidence.sqlite"), []byte("corrupt preexisting capture"), 0600); err != nil {
			return nil, err
		}
		return agenttest.NewHarness(ctx, func(context.Context, *runtime.Runtime) error {
			return nil
		}, opts...)
	})
	h, err := factory(t.Context(), AttemptInfo{Key: writer.keys[0]})
	require.ErrorIs(t, err, os.ErrExist)
	assert.Nil(t, h)
	require.NoError(t, writer.Err())
	rows, err := store.ReadEvidence(t.Context(), writer.ID())
	require.Error(t, err)
	require.Len(t, rows, 1)
	assert.NotEmpty(t, rows[0].Error)
}
