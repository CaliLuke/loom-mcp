package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedSealLog struct{ runlog.Store }

func TestContinuationNeverPublishesBeforeSeal(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	start := continuationTestStart(t, rt, "r1", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}})
	rt.RunEventStore = failedSealLog{rt.RunEventStore}
	_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{RootRunID: "r1", RunID: "r1", SessionID: "session", Pos: 1, Publish: true, ProtocolVersion: api.WorkflowProtocolVersion, IndexHead: start.IndexHead, EventKey: "run_sealed:r1", Timestamp: time.Unix(2, 0)})
	require.ErrorContains(t, err, "seal append fault")
	head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	assert.Nil(t, head)
}
func (s failedSealLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	if event.Type == hooks.RunSealed {
		return runlog.AppendResult{}, errors.New("seal append fault")
	}
	return s.Store.Append(ctx, event)
}
