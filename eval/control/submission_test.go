package control_test

import (
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestControlSubmissionAdmissionAndSafeErrors(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	q := control.SubmitRequest{Candidate: f.Candidate, TaskID: "task", RequestKey: "request"}
	for _, kind := range []string{"draft", "task", "evaluation", "plan", "key"} {
		bad := q
		switch kind {
		case "draft":
			bad.Candidate = d.DraftID
		case "task":
			bad.TaskID = "unknown"
		case "evaluation":
			bad.Kind = eval.ExecutionEvaluation
		case "plan":
			bad.EvaluationPlan = "forbidden"
		case "key":
			bad.RequestKey = "\n"
		}
		_, err = s.Submit(t.Context(), bad)
		require.Error(t, err)
	}
	e := cfg.Executor.(*executor)
	assert.Zero(t, e.accepts)
	job, err := s.Submit(t.Context(), q)
	require.NoError(t, err)
	assert.Equal(t, control.Queued, job.State)
	assert.Equal(t, eval.ExecutionProbe, e.record.Submission.Kind)
	replay, err := s.Submit(t.Context(), q)
	require.NoError(t, err)
	assert.Equal(t, job, replay)
	e.failure = &control.Error{Code: control.InvalidInput, Message: "compiler /private/file", Recovery: "read /private/file", Field: "/private/file"}
	_, err = s.Submit(t.Context(), q)
	code(t, err, control.InvalidInput)
	assert.NotContains(t, err.Error(), "private")
	assert.NotContains(t, err.Error(), "compiler")
	e.failure = nil
	e.record.Submission.Scope = "other"
	_, err = s.Status(t.Context(), control.JobRequest{JobID: job.ID})
	code(t, err, control.NotFound)
}
