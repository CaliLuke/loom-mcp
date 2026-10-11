package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sealLostAckStore struct {
	runlog.Store
	lost bool
}

func TestSealRecordRetry(t *testing.T) {
	rt := New()
	base := testWorkflowInput(t, rt, &planner.PlanInput{RunContext: run.Context{RootRunID: "root", RunID: "run"}, Messages: []*model.Message{contentMessage("seed")}})
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	base.producingCall = &key
	transcript, err := rt.RunStore.Put(t.Context(), "root", mustSealTranscript(t))
	require.NoError(t, err)
	require.NoError(t, base.recordAssistant(transcript, nil))
	input := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", IndexHead: base.IndexHead, Pending: base.view().Pending, EventKey: "run_sealed:run", Timestamp: time.Unix(42, 123456789).UTC()}
	before, err := json.Marshal(input)
	require.NoError(t, err)
	lost := &sealLostAckStore{Store: rt.RunEventStore}
	rt.RunEventStore = lost
	_, err = rt.SealRecordActivity(t.Context(), input)
	require.ErrorContains(t, err, "seal acknowledgement lost")
	out, err := rt.SealRecordActivity(t.Context(), input)
	require.NoError(t, err)
	again, err := rt.SealRecordActivity(t.Context(), input)
	require.NoError(t, err)
	assert.Equal(t, out, again)
	after, err := json.Marshal(input)
	require.NoError(t, err)
	assert.Equal(t, before, after, "retry input remains unchanged")
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	event := page.Events[0]
	assert.Equal(t, hooks.RunSealed, event.Type)
	assert.Equal(t, input.EventKey, event.EventKey)
	assert.Equal(t, input.Timestamp, event.Timestamp)
	var payload hooks.RunSealedEvent
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	assert.Equal(t, out.IndexHead, payload.IndexHead)
	assert.Equal(t, input.ProtocolVersion, payload.ProtocolVersion)
	entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", hash)
	}, out.IndexHead)
	require.NoError(t, err)
	assert.Equal(t, base.Entries, entries)
	assert.Equal(t, &key, entries[1].Call)
}

func TestSealRecordFailsBeforeAppend(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{RunContext: run.Context{RootRunID: "root", RunID: "run"}}
	require.NoError(t, base.structural(index.HideBookkeeping, []string{"call"}))
	input := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", Pending: base.view().Pending, EventKey: "run_sealed:run", Timestamp: time.Unix(42, 0)}
	rt.RunStore = failingContentStore{rt.RunStore}
	_, err := rt.SealRecordActivity(t.Context(), input)
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	assert.Empty(t, page.Events)
}

func TestSealRecordRequiredInput(t *testing.T) {
	for _, input := range []*api.SealRecordInput{nil, {}, {RootRunID: "root"}, {RootRunID: "root", RunID: "run", EventKey: "random"}} {
		rt := New()
		_, err := rt.SealRecordActivity(t.Context(), input)
		require.Error(t, err)
	}
	input := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", EventKey: "run_sealed:run", Timestamp: time.Unix(42, 0)}
	rt := New()
	rt.RunStore = nil
	_, err := rt.SealRecordActivity(t.Context(), input)
	require.ErrorContains(t, err, "run store and run event store are required")
	rt = New()
	rt.RunEventStore = nil
	_, err = rt.SealRecordActivity(t.Context(), input)
	require.ErrorContains(t, err, "run store and run event store are required")
}

func mustSealTranscript(t *testing.T) []byte {
	t.Helper()
	message := contentMessage("final")
	message.Role = model.ConversationRoleAssistant
	data, err := json.Marshal([]*model.Message{message})
	require.NoError(t, err)
	return data
}

func (s *sealLostAckStore) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	result, err := s.Store.Append(ctx, event)
	if err != nil {
		return result, err
	}
	if !s.lost {
		s.lost = true
		return result, errors.New("seal acknowledgement lost")
	}
	return result, nil
}
