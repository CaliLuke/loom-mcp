package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextErrorExecutor struct{ err error }

func TestControlHostContextErrorsExcludeJoinedDiagnostics(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			cfg := config(t)
			cfg.Executor = &contextErrorExecutor{err: errors.Join(sentinel, errors.New("secret /host/credentials"))}
			s := service(t, cfg)
			require.NoError(t, t.Context().Err(), "host cancellation must not be confused with caller cancellation")
			_, err := s.Status(t.Context(), control.JobRequest{JobID: "job"})
			require.ErrorIs(t, err, sentinel)
			assert.Equal(t, sentinel.Error(), err.Error())
			assert.NotContains(t, err.Error(), "secret")
			reg := native(t, s)
			out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: "experiment.get_job", ToolCallID: "context-failure", Payload: rawjson.Message(`{"job_id":"job"}`)})
			assert.Nil(t, out, "host context failure must not fabricate a job or tool result")
			require.ErrorIs(t, err, sentinel)
			assert.Equal(t, sentinel.Error(), err.Error())
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func (e *contextErrorExecutor) Status(context.Context, string, string) (control.JobRecord, error) {
	return control.JobRecord{}, e.err
}
func (e *contextErrorExecutor) Cancel(context.Context, string, string) (control.JobRecord, error) {
	return control.JobRecord{}, e.err
}

func (e *contextErrorExecutor) Accept(context.Context, control.Submission) (control.JobRecord, error) {
	return control.JobRecord{}, e.err
}
