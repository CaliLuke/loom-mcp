package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lifecycleTestReceipt() PreparedReceipt {
	return PreparedReceipt{CandidateDigest: "sha256:" + strings.Repeat("a", 64), SurfaceID: "test", SurfaceVersion: "1", Bindings: []PreparedBinding{{ID: "test", Version: "1"}}, TaskSnapshot: "task", SourceSnapshot: "source", InputSnapshot: "inputs", ModelConfig: "scripted", Environment: EnvironmentIdentity{Mode: "fixture", SnapshotID: "fixture"}}
}

func preparedTestLease(order *[]string) *PreparedAttempt {
	return &PreparedAttempt{
		Harness: AttemptHarnessConfig{Register: registerReportTools, Options: []agenttest.Option{agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("done")))}},
		Inputs:  []agenttest.Input{agenttest.User("hello")}, Receipt: lifecycleTestReceipt(),
		Deliverables: []Deliverable{{Name: "answer.txt", MediaType: "text/plain"}},
		Quiesce: func(context.Context) error {
			*order = append(*order, "quiesce")
			return nil
		},
		Collect: func(_ context.Context, input CollectionInput) ([]CollectedArtifact, error) {
			*order = append(*order, "collect")
			if input.EvidenceError != nil {
				return nil, input.EvidenceError
			}
			return []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain", Data: []byte("done")}}, nil
		},
		Release: func(context.Context) error {
			*order = append(*order, "release")
			return nil
		},
	}
}

func preparedTestConfig(prepare PrepareAttemptFunc) Config {
	cfg := storeConfig()
	cfg.Harness = nil
	cfg.Scenarios[0].Agent = "report"
	cfg.Scenarios[0].Task = "default task"
	cfg.Scenarios[0].Reference = "default reference"
	cfg.PrepareAttempt = prepare
	cfg.PrepareTimeout = time.Second
	cfg.CleanupTimeout = time.Second
	return cfg
}

func beginPreparedTest(t *testing.T, cfg *Config) (*Store, *Writer) {
	t.Helper()
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), *cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	cfg.PreparedWriter = writer
	return store, writer
}

func TestPreparedLifecycleFailures(t *testing.T) {
	for _, phase := range []string{"success", "close", "prepare", "nil lease", "prepare panic", "register", "setup", "setup panic", "inputs", "drive", "drive panic", "quiesce", "quiesce panic", "collect", "collect panic", "release", "release panic", "missing deliverable", "empty deliverable"} {
		t.Run(phase, func(t *testing.T) {
			var order []string
			lease := preparedTestLease(&order)
			cfg := preparedTestConfig(func(_ context.Context, info PreparedAttemptInfo) (*PreparedAttempt, error) {
				order = append(order, "prepare")
				assert.Equal(t, "default task", info.Item.Task)
				assert.Equal(t, "default reference", info.Item.Reference)
				if phase == "prepare" {
					return lease, assert.AnError
				}
				if phase == "nil lease" {
					return nil, nil
				}
				if phase == "prepare panic" {
					panic("prepare broken")
				}
				return lease, nil
			})
			switch phase {
			case "close":
				lease.Harness.Options = append(lease.Harness.Options, agenttest.ScriptedModel("unused", agenttest.Respond(agenttest.Text("unused"))))
			case "register":
				lease.Harness.Register = func(context.Context, *runtime.Runtime) error {
					return assert.AnError
				}
			case "setup", "setup panic":
				lease.Harness.Setup = func(*agenttest.Harness) error {
					if phase == "setup panic" {
						panic("setup broken")
					}
					return assert.AnError
				}
			case "inputs":
				lease.Inputs = []agenttest.Input{{nil}}
			case "drive", "drive panic":
				lease.Inputs = nil
				lease.Receipt.DriverVersion = "1"
				lease.Drive = func(context.Context, *agenttest.Harness, Item) (*agenttest.SessionResult, error) {
					if phase == "drive panic" {
						panic("drive broken")
					}
					return nil, assert.AnError
				}
			case "quiesce", "quiesce panic":
				lease.Quiesce = func(context.Context) error {
					order = append(order, "quiesce")
					if phase == "quiesce panic" {
						panic("quiesce broken")
					}
					return assert.AnError
				}
			case "collect", "collect panic", "missing deliverable", "empty deliverable":
				lease.Collect = func(context.Context, CollectionInput) ([]CollectedArtifact, error) {
					order = append(order, "collect")
					if phase == "collect panic" {
						panic("collect broken")
					}
					if phase == "collect" {
						return nil, assert.AnError
					}
					if phase == "missing deliverable" {
						return nil, nil
					}
					return []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain"}}, nil
				}
			case "release", "release panic":
				lease.Release = func(context.Context) error {
					order = append(order, "release")
					if phase == "release panic" {
						panic("release broken")
					}
					return assert.AnError
				}
			}
			store, writer := beginPreparedTest(t, &cfg)
			report, runErr := Run(t.Context(), cfg)
			require.NoError(t, runErr)
			require.Len(t, report.Attempts, 1)
			r := report.Attempts[0]
			if phase == "success" || phase == "empty deliverable" {
				assert.Equal(t, VerdictPassed, r.Verdict())
				require.NoError(t, r.HarnessErr)
			} else {
				assert.Equal(t, VerdictError, r.Verdict())
			}
			if phase != "nil lease" && phase != "prepare panic" {
				assert.Equal(t, []string{"prepare", "quiesce", "collect", "release"}, order)
			} else {
				assert.Equal(t, []string{"prepare"}, order)
			}
			require.NoError(t, writer.Finish(report, runErr))
			loaded, err := store.Load(writer.ID())
			require.NoError(t, err)
			if phase == "success" || phase == "empty deliverable" || strings.HasPrefix(phase, "quiesce") || strings.HasPrefix(phase, "release") {
				data, err := store.ReadArtifact(t.Context(), writer.ID(), r.AttemptKey, "answer.txt")
				require.NoError(t, err)
				if phase == "empty deliverable" {
					assert.Empty(t, data)
				} else {
					assert.Equal(t, "done", string(data))
				}
				assert.Equal(t, strings.HasPrefix(phase, "quiesce"), loaded.Attempts[0].Artifacts[0].Provisional)
			}
		})
	}
}

func TestPreparedInputsOwnedBeforeRegistration(t *testing.T) {
	var order []string
	lease := preparedTestLease(&order)
	lease.Harness.Register = func(ctx context.Context, rt *runtime.Runtime) error {
		lease.Inputs[0][0].Parts[0] = model.TextPart{Text: "mutated"}
		return registerReportTools(ctx, rt)
	}
	lease.Collect = func(_ context.Context, input CollectionInput) ([]CollectedArtifact, error) {
		require.NotNil(t, input.Evidence)
		request := input.Evidence.Records[0].Runs[0].Calls[0].Request
		assert.Equal(t, model.TextPart{Text: "hello"}, request.Messages[0].Parts[0])
		return []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain"}}, nil
	}
	cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		return lease, nil
	})
	beginPreparedTest(t, &cfg)
	r, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.NoError(t, r.Attempts[0].HarnessErr)
}

func TestPreparedCancellationCleanupAllowances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var order []string
		lease := preparedTestLease(&order)
		lease.Inputs = nil
		lease.Receipt.DriverVersion = "1"
		lease.Drive = func(ctx context.Context, _ *agenttest.Harness, _ Item) (*agenttest.SessionResult, error) {
			cancel()
			return nil, ctx.Err()
		}
		lease.Quiesce = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			<-ctx.Done()
			order = append(order, "quiesce")
			return ctx.Err()
		}
		lease.Collect = func(ctx context.Context, input CollectionInput) ([]CollectedArtifact, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			assert.True(t, input.Provisional)
			<-ctx.Done()
			order = append(order, "collect")
			return nil, ctx.Err()
		}
		lease.Release = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.Equal(t, time.Second, time.Until(deadline))
			order = append(order, "release")
			return nil
		}
		cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
			return lease, nil
		})
		beginPreparedTest(t, &cfg)
		report, err := Run(ctx, cfg)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, report.Attempts[0].Outcome.Err, context.Canceled)
		require.ErrorIs(t, report.Attempts[0].HarnessErr, context.DeadlineExceeded)
		assert.Equal(t, []string{"quiesce", "collect", "release"}, order)
	})
}

func TestPreparedReceiptFailurePreventsRegistration(t *testing.T) {
	var order []string
	registered := false
	lease := preparedTestLease(&order)
	lease.Harness.Register = func(context.Context, *runtime.Runtime) error {
		registered = true
		return nil
	}
	var writer *Writer
	cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		// Sabotage only receipt publication, after evidence reservation succeeded.
		err := os.Mkdir(filepath.Join(writer.dir, "evidence", "0", "receipt.json"), 0700)
		return lease, err
	})
	_, writer = beginPreparedTest(t, &cfg)
	r, err := Run(t.Context(), cfg)
	require.Error(t, err)
	assert.False(t, registered)
	require.Error(t, r.Attempts[0].HarnessErr)
	assert.Equal(t, []string{"quiesce", "collect", "release"}, order)
	assert.ErrorIs(t, err, writer.Err())
}
