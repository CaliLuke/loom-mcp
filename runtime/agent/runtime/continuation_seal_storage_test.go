package runtime

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationSealStorageFailureCanRetryWithoutPartialPublication(t *testing.T) {
	baseline, input := sealStorageFixture(t)
	trace := &continuationStorageFault{Store: baseline.RunStore}
	baseline.RunStore = trace
	_, err := baseline.SealRecordActivity(t.Context(), input)
	require.NoError(t, err)
	for i, operation := range trace.operations {
		t.Run(fmt.Sprintf("%02d_%s", i+1, operation), func(t *testing.T) {
			rt, in := sealStorageFixture(t)
			failed := &continuationStorageFault{Store: rt.RunStore, failAt: i + 1}
			rt.RunStore = failed
			out, err := rt.SealRecordActivity(t.Context(), in)
			require.ErrorIs(t, err, assert.AnError)
			assert.Nil(t, out)
			head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
			require.NoError(t, err)
			assert.Nil(t, head)
			failed.failAt = 0
			_, err = rt.SealRecordActivity(t.Context(), in)
			require.NoError(t, err)
			head, err = rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
			require.NoError(t, err)
			require.NotNil(t, head)
			assert.Equal(t, "root", head.ID)
		})
	}
}

func sealStorageFixture(t *testing.T) (*Runtime, *api.SealRecordInput) {
	t.Helper()
	rt := New()
	_, err := rt.CreateSession(t.Context(), "session")
	require.NoError(t, err)
	start := continuationTestStart(t, rt, "root", []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("remember ", 40000)}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("answer ", 40000)}}},
	})
	return rt, &api.SealRecordInput{RootRunID: "root", RunID: "root", SessionID: "session", Pos: 1, Publish: true, ProtocolVersion: api.WorkflowProtocolVersion, IndexHead: start.IndexHead, EventKey: "run_sealed:root", Timestamp: time.Unix(2, 0)}
}
