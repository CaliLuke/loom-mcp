package control_test

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlJobRecoveryGuidance(t *testing.T) {
	for _, tc := range []struct {
		state control.JobState
		want  string
	}{
		{control.Failed, "new request key"},
		{control.Interrupted, "new request key"},
		{control.Queued, "Poll the original job"},
		{control.Running, "Poll the original job"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			cfg := config(t)
			s := service(t, cfg)
			d := draft(t, s, cfg)
			f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
			require.NoError(t, err)
			q := control.SubmitRequest{Candidate: f.Candidate, TaskID: "task", RequestKey: "request"}
			job, err := s.Submit(t.Context(), q)
			require.NoError(t, err)
			e := cfg.Executor.(*executor)
			e.record.State = tc.state
			e.record.Manifest = "retained-evidence"
			e.record.Failure = &control.Error{Code: control.OperationFailed, Message: "private provider body", Recovery: "private token", Field: "/private/path"}
			status, err := s.Status(t.Context(), control.JobRequest{JobID: job.ID})
			require.NoError(t, err)
			require.NotNil(t, status.Failure)
			assert.Contains(t, status.Failure.Recovery, tc.want)
			assert.Contains(t, status.Failure.Error(), tc.want)
			assert.True(t, status.EvidenceAvailable)
			replay, err := s.Submit(t.Context(), q)
			require.NoError(t, err)
			assert.Equal(t, status, replay)
			assert.Equal(t, "retained-evidence", string(e.record.Manifest))
			data, err := json.Marshal(replay)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "private")
			assert.NotContains(t, status.Failure.Error(), "private")
		})
	}
}

func TestControlUnknownAdmissionKeepsOriginalKey(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	cfg.Executor.(*executor).failure = errors.New("private transport failure")
	_, err = s.Submit(t.Context(), control.SubmitRequest{Candidate: f.Candidate, TaskID: "task", RequestKey: "request"})
	code(t, err, control.OperationFailed)
	assert.Contains(t, err.Error(), "same request key")
	assert.NotContains(t, err.Error(), "private")
}
