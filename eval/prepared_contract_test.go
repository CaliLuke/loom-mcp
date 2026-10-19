package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedWriterRejectedBeforeAcquisition(t *testing.T) {
	for _, kind := range []string{"missing", "wrong plan", "wrong settings", "wrong timeout", "wrong provenance", "finished", "sticky", "reservation"} {
		t.Run(kind, func(t *testing.T) {
			acquired := false
			cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
				acquired = true
				return nil, assert.AnError
			})
			_, writer := beginPreparedTest(t, &cfg)
			switch kind {
			case "missing":
				cfg.PreparedWriter = nil
			case "wrong plan":
				cfg.Repeats = 2
			case "wrong settings":
				cfg.MaxTokens = 5
			case "wrong timeout":
				cfg.CleanupTimeout *= 2
			case "wrong provenance":
				cfg.Scenarios[0].Items = Items(Item{ID: "one", Input: "different"})
			case "finished":
				writer.finished = true
			case "sticky":
				writer.err = assert.AnError
			case "reservation":
				require.NoError(t, os.Mkdir(filepath.Join(writer.dir, "evidence"), 0700))
				require.NoError(t, os.Mkdir(filepath.Join(writer.dir, "evidence", "0"), 0700))
			}
			_, err := Run(t.Context(), cfg)
			require.Error(t, err)
			assert.False(t, acquired)
		})
	}
}

func TestPreparedInvalidLeaseBeforeRegistration(t *testing.T) {
	for _, kind := range []string{"missing register", "both drivers", "no driver", "empty inputs", "no agent", "driver identity", "release", "quiesce", "collect", "receipt", "declarations", "base capture", "configured capture", "variant capture"} {
		t.Run(kind, func(t *testing.T) {
			var order []string
			lease := preparedTestLease(&order)
			registered := false
			lease.Harness.Register = func(context.Context, *runtime.Runtime) error {
				registered = true
				return nil
			}
			cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
				return lease, nil
			})
			switch kind {
			case "missing register":
				lease.Harness.Register = nil
			case "both drivers", "driver identity":
				lease.Drive = func(context.Context, *agenttest.Harness, Item) (*agenttest.SessionResult, error) {
					return nil, nil
				}
				if kind == "driver identity" {
					lease.Inputs = nil
				}
			case "no driver":
				lease.Inputs = nil
			case "empty inputs":
				lease.Inputs = []agenttest.Input{}
			case "no agent":
				cfg.Scenarios[0].Agent = ""
			case "release":
				lease.Release = nil
			case "quiesce":
				lease.Quiesce = nil
			case "collect":
				lease.Collect = nil
			case "receipt":
				lease.Receipt.InputSnapshot = ""
			case "declarations":
				lease.Deliverables = []Deliverable{{Name: "../escape", MediaType: "text/plain"}}
			case "base capture":
				lease.BaseOptions = []agenttest.Option{agenttest.WithEvidenceDir(t.TempDir())}
			case "configured capture":
				lease.Harness.Options = append(lease.Harness.Options, agenttest.WithEvidenceDir(t.TempDir()))
			case "variant capture":
				cfg.Variants = []Variant{{ID: DefaultVariant, Options: []agenttest.Option{agenttest.WithEvidenceDir(t.TempDir())}}}
			}
			beginPreparedTest(t, &cfg)
			r, err := Run(t.Context(), cfg)
			require.NoError(t, err)
			assert.False(t, registered)
			require.Error(t, r.Attempts[0].HarnessErr)
			assert.Equal(t, VerdictError, r.Attempts[0].Verdict())
			if kind != "release" {
				assert.Equal(t, "release", order[len(order)-1])
			}
		})
	}
}

func TestPreparedTimeoutReturnsPartialLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order []string
		lease := preparedTestLease(&order)
		cfg := preparedTestConfig(func(ctx context.Context, _ PreparedAttemptInfo) (*PreparedAttempt, error) {
			<-ctx.Done()
			return lease, nil
		})
		beginPreparedTest(t, &cfg)
		r, err := Run(t.Context(), cfg)
		require.NoError(t, err)
		require.ErrorIs(t, r.Attempts[0].HarnessErr, context.DeadlineExceeded)
		assert.NotNil(t, r.Attempts[0].Prepared)
		assert.Equal(t, []string{"quiesce", "collect", "release"}, order)
	})
}

func TestPreparedNoAgentCustomDriver(t *testing.T) {
	var order []string
	lease := preparedTestLease(&order)
	lease.Inputs = nil
	lease.Receipt.DriverVersion = "1"
	lease.Drive = func(ctx context.Context, h *agenttest.Harness, item Item) (*agenttest.SessionResult, error) {
		assert.Equal(t, "default task", item.Task)
		return h.RunSession(ctx, "report", agenttest.User(item.Input))
	}
	cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		return lease, nil
	})
	cfg.Scenarios[0].Agent = ""
	beginPreparedTest(t, &cfg)
	r, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, VerdictPassed, r.Attempts[0].Verdict())
}

func TestPreparedCollectInvalidResults(t *testing.T) {
	for _, artifacts := range [][]CollectedArtifact{
		{{Name: "other.txt", MediaType: "text/plain"}},
		{{Name: "answer.txt", MediaType: "application/json"}},
		{{Name: "answer.txt", MediaType: "text/plain"}, {Name: "answer.txt", MediaType: "text/plain"}},
	} {
		var order []string
		lease := preparedTestLease(&order)
		lease.Collect = func(context.Context, CollectionInput) ([]CollectedArtifact, error) {
			return artifacts, nil
		}
		cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
			return lease, nil
		})
		beginPreparedTest(t, &cfg)
		r, err := Run(t.Context(), cfg)
		require.NoError(t, err)
		require.Error(t, r.Attempts[0].HarnessErr)
		assert.Equal(t, "release", order[len(order)-1])
	}
}

func TestPreparedOptionOrdering(t *testing.T) {
	for _, variant := range []bool{false, true} {
		var order []string
		lease := preparedTestLease(&order)
		lease.BaseOptions = []agenttest.Option{agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("base")))}
		lease.Harness.Options = []agenttest.Option{agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("configured")))}
		cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
			return lease, nil
		})
		want := "configured"
		if variant {
			cfg.Variants = []Variant{{ID: DefaultVariant, Options: []agenttest.Option{agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("variant")))}}}
			want = "variant"
		}
		beginPreparedTest(t, &cfg)
		r, err := Run(t.Context(), cfg)
		require.NoError(t, err)
		require.NoError(t, r.Attempts[0].HarnessErr)
		assert.Equal(t, want, r.Attempts[0].Outcome.Session.Runs[0].Output.Final.Parts[0].(model.TextPart).Text)
	}
}

func TestPreparedCollectionSnapshotOwned(t *testing.T) {
	var order []string
	lease := preparedTestLease(&order)
	lease.Collect = func(_ context.Context, input CollectionInput) ([]CollectedArtifact, error) {
		input.Receipt.Bindings[0].Version = "tampered"
		input.Receipt.CandidateDigest = "changed"
		input.Evidence.Records[0].Runs[0].Calls[0].Request.Messages[0].Parts[0] = model.TextPart{Text: "changed"}
		return []CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain"}}, nil
	}
	cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		return lease, nil
	})
	store, writer := beginPreparedTest(t, &cfg)
	r, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, r.Attempts[0].HarnessErr)
	require.NoError(t, writer.Finish(r, err))
	loaded, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.Equal(t, lifecycleTestReceipt(), *loaded.Attempts[0].Prepared)
	rows, err := store.ReadEvidence(t.Context(), writer.ID())
	require.NoError(t, err)
	assert.Equal(t, model.TextPart{Text: "hello"}, rows[0].Snapshot.Records[0].Runs[0].Calls[0].Request.Messages[0].Parts[0])
}

func TestPreparedCloseBeforeQuiescence(t *testing.T) {
	for _, setupFailure := range []bool{false, true} {
		var order []string
		lease := preparedTestLease(&order)
		var constructed *agenttest.Harness
		lease.Harness.Setup = func(h *agenttest.Harness) error {
			constructed = h
			if setupFailure {
				return assert.AnError
			}
			return nil
		}
		lease.Quiesce = func(ctx context.Context) error {
			if constructed == nil {
				return errors.New("construction not observed")
			}
			_, err := constructed.Run(ctx, "report", nil)
			assert.ErrorContains(t, err, "closed")
			return nil
		}
		cfg := preparedTestConfig(func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
			return lease, nil
		})
		beginPreparedTest(t, &cfg)
		r, err := Run(t.Context(), cfg)
		require.NoError(t, err)
		if setupFailure {
			assert.ErrorIs(t, r.Attempts[0].HarnessErr, assert.AnError)
		} else {
			assert.NoError(t, r.Attempts[0].HarnessErr)
		}
	}
}
