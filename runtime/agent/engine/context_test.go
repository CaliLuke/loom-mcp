package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowAndActivityContextContracts(t *testing.T) {
	base := context.Background()
	wf := &stubWorkflowContext{}
	ctx := WithWorkflowContext(base, wf)
	assert.Same(t, wf, WorkflowContextFromContext(ctx))
	assert.Nil(t, WorkflowContextFromContext(base))
	assert.Nil(t, WorkflowContextFromContext(context.WithValue(base, wfCtxKey{}, "wrong type")))

	assert.False(t, IsActivityContext(base))
	assert.True(t, IsActivityContext(WithActivityContext(base)))
	assert.False(t, IsActivityContext(context.WithValue(base, activityCtxKey{}, "true")))
}

func TestActivityHeartbeatContextContracts(t *testing.T) {
	base := context.Background()
	recorder := &recordingHeartbeat{}
	ctx := WithActivityHeartbeatRecorder(base, recorder)

	assert.True(t, RecordActivityHeartbeat(ctx, "step", 2))
	assert.Equal(t, []any{"step", 2}, recorder.details)
	assert.False(t, RecordActivityHeartbeat(base, "ignored"))
	assert.False(t, RecordActivityHeartbeat(context.WithValue(base, activityHeartbeatKey{}, nil), "ignored"))

	assert.Zero(t, ActivityHeartbeatTimeout(base))
	assert.Zero(t, ActivityHeartbeatTimeout(context.WithValue(base, activityHeartbeatTimeoutKey{}, "1s")))
	assert.Equal(t, 3*time.Second, ActivityHeartbeatTimeout(WithActivityHeartbeatTimeout(base, 3*time.Second)))
}

type stubWorkflowContext struct {
	WorkflowContext
}

type recordingHeartbeat struct {
	details []any
}

func (r *recordingHeartbeat) RecordHeartbeat(details ...any) {
	r.details = append([]any(nil), details...)
}
