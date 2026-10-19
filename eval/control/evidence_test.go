package control_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func beginEvidence(t *testing.T, cfg control.Config) (*eval.Writer, eval.AttemptKey) {
	t.Helper()
	key := eval.AttemptKey{Scenario: "task", Item: "item", Variant: "candidate", Repeat: 1}
	pc := eval.ProbeConfig{Key: key, Item: eval.Item{ID: "item", Input: "input"}, Variant: eval.Variant{ID: "candidate"}, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return nil, errors.New("not executed")
	}}
	w, err := cfg.Experiments.BeginProbe(t.Context(), pc, eval.ExperimentMeta{})
	require.NoError(t, err)
	return w, key
}
func TestControlEvidenceNewJobBoundViews(t *testing.T) {
	cfg := config(t)
	cfg.Operations = append(cfg.Operations, control.ListEvidenceOperation, control.ReadEvidenceOperation)
	dir := t.TempDir()
	ws, err := inspect.Open(dir, cfg.Experiments, cfg.Candidates, inspect.DefaultLimits())
	require.NoError(t, err)
	e := cfg.Executor.(*executor)
	views := 0
	cfg.Evidence = func(ctx context.Context, id string) (*inspect.View, error) {
		r, err := e.Status(ctx, cfg.Scope, id)
		if err != nil {
			return nil, err
		}
		views++
		return ws.View(inspect.Scope{Manifests: []inspect.ManifestRef{r.Manifest}}, inspect.ViewPolicy{Role: inspect.Editor, Name: "editor-v1"})
	}
	s := service(t, cfg)
	d := draft(t, s, cfg)
	f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	job, err := s.Submit(t.Context(), control.SubmitRequest{Candidate: f.Candidate, TaskID: "task", RequestKey: "evidence"})
	require.NoError(t, err)
	_, err = s.ListEvidence(t.Context(), control.EvidenceListRequest{JobID: job.ID})
	code(t, err, control.EvidenceNotReady)
	assert.Zero(t, views)
	writer, key := beginEvidence(t, cfg)
	publication := inspect.Publication{Origin: inspect.Origin{ExperimentID: writer.ID(), Key: key}, CandidateDigest: f.Candidate, Policies: []inspect.Policy{{Role: inspect.Editor, Name: "editor-v1"}}, Attachments: []inspect.Attachment{{Name: "unopened.txt", Kind: inspect.TaskSource, MediaType: "text/plain", Data: []byte("retained source"), Audiences: []inspect.Role{inspect.Editor}}}}
	ref, err := ws.Publish(t.Context(), publication)
	require.NoError(t, err)
	// Publication alone is not access: job association has not committed.
	_, err = s.ListEvidence(t.Context(), control.EvidenceListRequest{JobID: job.ID})
	code(t, err, control.EvidenceNotReady)
	e.record.Manifest = ref
	e.record.ExperimentID = writer.ID()
	e.record.Attempt = key
	page, err := s.ListEvidence(t.Context(), control.EvidenceListRequest{JobID: job.ID})
	require.NoError(t, err)
	var locator string
	for _, entry := range page.Entries {
		if entry.Kind == inspect.TaskSource {
			locator = entry.Locator
		}
	}
	require.NotEmpty(t, locator)
	read, err := s.ReadEvidence(t.Context(), control.EvidenceReadRequest{JobID: job.ID, Locator: locator, MaxBytes: 3})
	require.NoError(t, err)
	assert.Equal(t, []byte("ret"), read.Data)
	assert.True(t, read.Truncated)
	assert.Equal(t, 2, views)
	reopened, err := inspect.Open(dir, cfg.Experiments, cfg.Candidates, inspect.DefaultLimits())
	require.NoError(t, err)
	ws = reopened
	read, err = s.ReadEvidence(t.Context(), control.EvidenceReadRequest{JobID: job.ID, Locator: locator})
	require.NoError(t, err)
	assert.Equal(t, []byte("retained source"), read.Data)
	// A second manifest under the same host cannot use the first manifest's locator.
	publication.Attachments[0].Data = []byte("another publication")
	other, err := ws.Publish(t.Context(), publication)
	require.NoError(t, err)
	e.record.Manifest = other
	_, err = s.ReadEvidence(t.Context(), control.EvidenceReadRequest{JobID: job.ID, Locator: locator})
	code(t, err, control.NotFound)
	cfg.Authorize = func(context.Context, control.Operation) error { return errors.New("revoked") }
	revoked := service(t, cfg)
	_, err = revoked.ReadEvidence(t.Context(), control.EvidenceReadRequest{JobID: job.ID, Locator: locator})
	code(t, err, control.NotFound)
}
