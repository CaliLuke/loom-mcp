package control_test

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlCandidateBoundariesAndFork(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	for _, q := range []control.CreateRequest{{}, {Baseline: cfg.Candidates.Discover().BaselineDigest, SourceCandidate: cfg.Candidates.Discover().BaselineDigest}, {Baseline: strings.Repeat("b", 64)}, {SourceCandidate: "../path"}} {
		_, err := s.Create(t.Context(), q)
		require.Error(t, err)
	}
	for _, q := range []control.GetCandidateRequest{{}, {DraftID: "invalid"}, {Candidate: "invalid"}, {DraftID: strings.Repeat("0", 32)}, {Candidate: strings.Repeat("0", 64)}} {
		_, err := s.GetCandidate(t.Context(), q)
		require.Error(t, err)
	}
	for _, q := range []control.ReadCandidateRequest{
		{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: "main.md", Offset: -1},
		{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: "main.md", MaxBytes: -1},
		{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: "main.md", Offset: 9},
		{DraftID: d.DraftID, ExpectedRevision: d.Revision + 1, Component: "main.md"},
		{DraftID: d.DraftID, Component: "main.md"},
		{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: "missing.md"},
		{Candidate: cfg.Candidates.Discover().BaselineDigest, ExpectedRevision: 1, Component: "main.md"},
		{Candidate: strings.Repeat("0", 64), Component: "main.md"},
	} {
		_, err := s.ReadCandidate(t.Context(), q)
		require.Error(t, err)
	}
	end, err := s.ReadCandidate(t.Context(), control.ReadCandidateRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: "main.md", Offset: 8})
	require.NoError(t, err)
	assert.Empty(t, end.Data)
	assert.False(t, end.Truncated)
	assert.Nil(t, end.NextOffset)
	f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	again, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	assert.Equal(t, f, again)
	info, err := s.GetCandidate(t.Context(), control.GetCandidateRequest{Candidate: f.Candidate})
	require.NoError(t, err)
	assert.Equal(t, f.Candidate, info.Candidate)
	fork, err := s.Create(t.Context(), control.CreateRequest{SourceCandidate: f.Candidate})
	require.NoError(t, err)
	assert.NotEqual(t, d.DraftID, fork.DraftID)
	_, err = s.Edit(t.Context(), control.EditRequest{DraftID: fork.DraftID, ExpectedRevision: fork.Revision, Operation: candidate.Delete, Name: "main.md"})
	require.NoError(t, err)
	current, err := s.GetCandidate(t.Context(), control.GetCandidateRequest{DraftID: fork.DraftID})
	require.NoError(t, err)
	assert.Empty(t, current.Components)
}
func TestControlCancellationProjectsHostState(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	e := cfg.Executor.(*executor)
	e.record = control.JobRecord{ID: "job", Submission: control.Submission{Scope: cfg.Scope}, State: control.Running}
	got, err := s.Cancel(t.Context(), control.JobRequest{JobID: "job"})
	require.NoError(t, err)
	assert.Equal(t, control.Canceled, got.State)
	e.record.State = control.Succeeded
	got, err = s.Cancel(t.Context(), control.JobRequest{JobID: "job"})
	require.NoError(t, err)
	assert.Equal(t, control.Succeeded, got.State)
	_, err = s.Cancel(t.Context(), control.JobRequest{JobID: "missing"})
	code(t, err, control.NotFound)
}
