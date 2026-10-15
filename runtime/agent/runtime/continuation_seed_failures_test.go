package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type continuationStorageFault struct {
	runstore.Store
	operations []string
	failAt     int
}

func TestContinuationSeedStorageFailuresAreRetryableWithoutPublication(t *testing.T) {
	original := New()
	_, err := original.CreateSession(t.Context(), "session")
	require.NoError(t, err)
	messages := make([]*model.Message, 0, 4)
	messages = append(messages, []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("remember ", 40000)}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("answer ", 40000)}}},
	}...)
	start := continuationTestStart(t, original, "prior", messages)
	continuationTestSeal(t, original, "prior", start, true)
	head, err := original.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	require.NotNil(t, head.Checkpoint, "fixture must exercise checkpoint copying")
	messages = append(messages, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}})
	items, err := encodeContentMessages(messages)
	require.NoError(t, err)
	// First observe the storage boundary count; each fault case runs the same public
	// activity with a fresh run identity and fails one actual store operation.
	baseline := &continuationStorageFault{Store: original.RunStore}
	rt := New(WithRunStore(baseline), WithSessionStore(original.SessionStore), WithRunEventStore(original.RunEventStore))
	probe := continuationSeedInput("probe", items)
	require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: "probe", SessionID: "session", AgentID: "a", Status: session.RunStatusRunning}))
	out, err := rt.RecordContentActivity(t.Context(), probe)
	require.NoError(t, err)
	require.Equal(t, "continued", out.Continuation.Outcome)
	require.NotEmpty(t, baseline.operations)
	for position, operation := range baseline.operations {
		t.Run(fmt.Sprintf("%02d_%s", position+1, operation), func(t *testing.T) {
			id := fmt.Sprintf("retry-%d", position)
			store := &continuationStorageFault{Store: original.RunStore, failAt: position + 1}
			runtime := New(WithRunStore(store), WithSessionStore(original.SessionStore), WithRunEventStore(original.RunEventStore))
			require.NoError(t, runtime.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: id, SessionID: "session", AgentID: "a", Status: session.RunStatusRunning}))
			input := continuationSeedInput(id, items)
			result, err := runtime.RecordContentActivity(t.Context(), input)
			require.ErrorIs(t, err, assert.AnError)
			assert.Nil(t, result)
			page, err := runtime.RunEventStore.List(t.Context(), id, "", 100)
			require.NoError(t, err)
			assert.Empty(t, page.Events, "failed seed must not publish continuation_started")
			after, err := runtime.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
			require.NoError(t, err)
			assert.Equal(t, head, after)
			store.failAt = 0
			retried, err := runtime.RecordContentActivity(t.Context(), input)
			require.NoError(t, err)
			assert.Equal(t, "continued", retried.Continuation.Outcome)
			assert.Equal(t, "prior", *retried.Continuation.Prev)
			assert.Equal(t, 2, retried.Continuation.Pos)
			replayed, err := runtime.RecordContentActivity(t.Context(), input)
			require.NoError(t, err)
			expected, err := json.Marshal(retried)
			require.NoError(t, err)
			actual, err := json.Marshal(replayed)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(actual))
			page, err = runtime.RunEventStore.List(t.Context(), id, "", 100)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			assert.Equal(t, hooks.ContinuationStarted, page.Events[0].Type)
		})
	}
}

func (s *continuationStorageFault) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.operations = append(s.operations, "get")
	if len(s.operations) == s.failAt {
		return nil, assert.AnError
	}
	return s.Store.Get(ctx, scope, hash)
}

func (s *continuationStorageFault) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	s.operations = append(s.operations, "put")
	if len(s.operations) == s.failAt {
		return "", assert.AnError
	}
	return s.Store.Put(ctx, scope, data)
}

func continuationSeedInput(id string, items [][]byte) *api.RecordContentInput {
	return &api.RecordContentInput{RootRunID: id, Items: items, SeedMessages: true, Continue: &api.ContinuationStartInput{SessionID: "session", RunID: id, AgentID: "a", Timestamp: time.Unix(3, 0)}}
}
