package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedFailedInputsRetainReceipt(t *testing.T) {
	for _, kind := range []string{"empty inputs", "clone failure", "invalid receipt", "receipt write failure"} {
		t.Run(kind, func(t *testing.T) {
			registered, released := 0, 0
			var order []string
			lease := preparedTestLease(&order)
			lease.Inputs = []agenttest.Input{}
			lease.Deliverables = nil
			lease.Harness.Register = func(context.Context, *runtime.Runtime) error {
				registered++
				return nil
			}
			lease.Collect = func(context.Context, CollectionInput) ([]CollectedArtifact, error) {
				return nil, nil
			}
			lease.Release = func(context.Context) error {
				released++
				return nil
			}
			if kind == "clone failure" {
				lease.Inputs = []agenttest.Input{{nil}}
			}
			if kind == "invalid receipt" {
				lease.Receipt.InputSnapshot = ""
			}
			var writer *Writer
			cfg := lifecycleProbeConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
				if kind == "receipt write failure" {
					if err := os.Mkdir(filepath.Join(writer.dir, "evidence", "0", "receipt.json"), 0700); err != nil {
						return lease, err
					}
				}
				return lease, nil
			})
			store, err := Open(t.TempDir())
			require.NoError(t, err)
			writer, err = store.BeginProbe(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
			require.NoError(t, err)
			cfg.PreparedWriter = writer
			report, runErr := Probe(t.Context(), cfg)
			require.NotNil(t, report)
			attempt := report.Attempts[0]
			require.ErrorContains(t, attempt.HarnessErr, "input")
			assert.Equal(t, 0, registered)
			assert.Equal(t, 1, released)
			assert.Equal(t, VerdictError, attempt.Verdict())
			if kind == "receipt write failure" {
				require.Error(t, runErr)
				require.ErrorIs(t, runErr, writer.Err())
				require.ErrorContains(t, attempt.HarnessErr, "receipt")
				assert.Nil(t, attempt.Prepared)
				return
			}
			require.NoError(t, runErr)
			require.NoError(t, writer.Finish(report, runErr))
			loaded, err := store.Load(writer.ID())
			require.NoError(t, err)
			if kind == "invalid receipt" {
				assert.Nil(t, loaded.Attempts[0].Prepared)
			} else {
				require.NotNil(t, loaded.Attempts[0].Prepared)
				assert.Equal(t, lease.Receipt, *loaded.Attempts[0].Prepared)
			}
		})
	}
}
