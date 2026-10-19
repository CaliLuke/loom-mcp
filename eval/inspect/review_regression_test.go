package inspect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncompleteDeliveryDoesNotCollideWithRetainedDeliveryName(t *testing.T) {
	f := newFixture(t)
	cfg := eval.ProbeConfig{Key: f.pub.Origin.Key, Item: eval.Item{ID: f.pub.Origin.Key.Item, Input: "input"}, Variant: eval.Variant{ID: f.pub.Origin.Key.Variant}, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return &eval.PreparedAttempt{
			Receipt:      eval.PreparedReceipt{CandidateDigest: f.pub.CandidateDigest, SurfaceID: "test", SurfaceVersion: "1", Bindings: []eval.PreparedBinding{{ID: "files", Version: "1"}}, TaskSnapshot: "task", SourceSnapshot: "source", InputSnapshot: "input", ModelConfig: "fixture", Environment: eval.EnvironmentIdentity{Mode: "fixture", SnapshotID: "snapshot"}},
			Deliverables: []eval.Deliverable{{Name: "delivery", MediaType: "text/plain"}, {Name: "other", MediaType: "text/plain"}},
			Quiesce:      func(context.Context) error { return nil }, Release: func(context.Context) error { return nil }, Collect: func(context.Context, eval.CollectionInput) ([]eval.CollectedArtifact, error) {
				return []eval.CollectedArtifact{{Name: "delivery", MediaType: "text/plain", Data: []byte("retained delivery")}}, nil
			},
		}, errors.New("preparation stopped after acquisition")
	}}
	writer, err := f.experiments.BeginProbe(t.Context(), cfg, eval.ExperimentMeta{})
	require.NoError(t, err)
	cfg.PreparedWriter = writer
	report, runErr := eval.Probe(t.Context(), cfg)
	require.NoError(t, runErr)
	require.NoError(t, writer.Finish(report, runErr))
	require.Equal(t, "incomplete", report.Attempts[0].Probe.Delivery)
	raw, err := f.experiments.ReadArtifact(t.Context(), writer.ID(), cfg.Key, "delivery")
	require.NoError(t, err)
	assert.Equal(t, "retained delivery", string(raw))
	f.pub.Origin.ExperimentID = writer.ID()
	f.pub.ExpectedDeliverables = nil
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	view, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	var retained, aggregate Entry
	for _, row := range rows {
		if row.Kind == Deliverable && row.State == Available {
			retained = row
		}
		if row.Code == "delivery_incomplete" {
			aggregate = row
		}
	}
	require.NotEmpty(t, retained.Locator)
	require.NotEmpty(t, aggregate.Locator)
	assert.NotEqual(t, retained.Locator, aggregate.Locator)
	body, err := view.Read(t.Context(), ReadQuery{Locator: retained.Locator})
	require.NoError(t, err)
	assert.Equal(t, raw, body.Data)
	assert.Equal(t, Missing, aggregate.State)
	assert.Nil(t, aggregate.Size)
	assert.Empty(t, aggregate.Digest)
}

func TestOversizedBlobRejectedBeforeReadAndRepeatPublication(t *testing.T) {
	f := newFixture(t)
	ref, err := f.w.Publish(t.Context(), f.pub)
	require.NoError(t, err)
	view, rows := entriesFor(t, f, ref, Editor, "editor/v1")
	source := entryOf(t, rows, TaskSource)
	root, err := os.OpenRoot(f.root)
	require.NoError(t, err)
	file, err := root.OpenFile(filepath.Join("blobs", source.Digest), os.O_WRONLY, 0600)
	require.NoError(t, err)
	require.NoError(t, root.Close())
	require.NoError(t, file.Truncate(64<<20))
	require.NoError(t, file.Close())
	data, err := f.w.readFile("blobs", source.Digest)
	require.ErrorIs(t, err, ErrIntegrity)
	assert.Empty(t, data)
	result, err := view.Read(t.Context(), ReadQuery{Locator: source.Locator, MaxBytes: 1})
	require.NoError(t, err)
	assert.Equal(t, Missing, result.Entry.State)
	assert.Equal(t, "integrity_failure", result.Entry.Code)
	assert.Empty(t, result.Data)
	_, err = f.w.Publish(t.Context(), f.pub)
	require.ErrorIs(t, err, ErrIntegrity)
}

func TestBodyReadBoundsGrowthAfterSizeCheck(t *testing.T) {
	reader := strings.NewReader(strings.Repeat("x", 4096))
	data, err := readBoundedBody(reader, 32)
	require.ErrorIs(t, err, ErrIntegrity)
	assert.Empty(t, data)
	assert.GreaterOrEqual(t, reader.Len(), 4096-33, "read at most the cap plus one growth-detection byte")
}
