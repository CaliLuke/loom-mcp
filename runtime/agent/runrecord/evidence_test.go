package runrecord_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingReadStore struct {
	runstore.Store
	failure error
}

type cyclicCheckpointStore struct {
	runstore.Store
	hash runstore.Hash
	data []byte
}

func TestSummaryChunksAndZombieAttempts(t *testing.T) {
	f := newRecordFixture()
	zombieDone := f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	zombieOpen := f.call(t, "run", 1, 1, runstore.CallStarted, callrecord.ActivityPlanner, "")
	producing := f.call(t, "run", 1, 2, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	aux := f.call(t, "run", 1, 2, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{producing, aux}, recordedProtocolVersion, time.Unix(1, 0)))
	// A zombie finishing after adoption must still appear in the reader's list.
	late := f.call(t, "run", 1, 1, runstore.CallFailed, callrecord.ActivityPlanner, "")
	chunks := []runstore.CallKey{f.call(t, "run", 2, 1, runstore.CallCompleted, callrecord.ActivitySummarize, ""), f.call(t, "run", 2, 1, runstore.CallFailed, callrecord.ActivitySummarize, "")}
	appendFakeSummaryOutput(t, f, chunks)
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 7)
	expected := map[runstore.CallKey]runrecord.CallClass{zombieDone: runrecord.Abandoned, zombieOpen: runrecord.Incomplete, producing: runrecord.Adopted, aux: runrecord.Adopted, late: runrecord.Abandoned, chunks[0]: runrecord.Adopted, chunks[1]: runrecord.Adopted}
	for _, call := range record.Runs[0].Calls {
		assert.Equal(t, expected[call.Record.Key], call.Class)
		assert.Positive(t, call.Step)
		assert.NotEmpty(t, call.Activity)
	}
}

func TestCheckpointResolutionAndExport(t *testing.T) {
	f := newRecordFixture()
	summary, err := f.store.Put(t.Context(), "root", []byte("summary"))
	require.NoError(t, err)
	earlier := f.put(t, index.CheckpointRecord{Summary: &summary, SummaryRun: "prior"})
	keys := []runstore.CallKey{f.call(t, "run", 2, 1, runstore.CallCompleted, callrecord.ActivitySummarize, "")}
	checkpoint := index.CheckpointRecord{
		Prev: &earlier, From: &index.RunCheckpoint{Scope: "root", Run: "prior", Hash: earlier},
		Earlier: index.EarlierReceipt{Runs: 1, Messages: 2, First: 1, Last: 1, Users: []index.IndexLine{{ID: 1, Text: "old user", Origin: &index.Origin{Scope: "root", Run: "prior", Pos: 1, ID: 1, Form: "full"}}}},
		Summary: &summary, SummaryRun: "run", CitedUsers: []index.IndexLine{{ID: 1, Text: "user"}}, Receipt: []index.ReceiptLine{{From: 1, Through: 2, Tool: "tool", Text: "args"}}, Through: 2, Chain: "chain", Prompt: &prompt.PromptRef{ID: "summary", Version: "1"}, Calls: keys,
	}
	hash := f.put(t, checkpoint)
	view := index.ViewRef{Checkpoint: &hash}
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 2, view, keys, recordedProtocolVersion, time.Unix(2, 0)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Checkpoints, 2)
	assert.Equal(t, checkpoint, record.Runs[0].Checkpoints[1].Record)
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
	// A checkpoint is resolved even when only an abandoned call's ViewRef names it.
	parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", &model.Request{Messages: []*model.Message{textMessage("message")}}, view, []*model.Message{textMessage("message")})
	require.NoError(t, err)
	assert.Equal(t, &hash, parts.View.Checkpoint)
}

func TestExportPreservesLargeMetadataInteger(t *testing.T) {
	f := newRecordFixture()
	req := &model.Request{Model: "fake", Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "integer"}}, Meta: map[string]any{"large": int64(9007199254740993)}}}}
	parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", req, index.ViewRef{}, nil)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	started, err := callrecord.Encode(callrecord.Started{Key: key, Activity: callrecord.ActivityPlanner, Step: 9, Model: "fake", Request: parts})
	require.NoError(t, err)
	require.NoError(t, f.store.PutCall(t.Context(), "root", runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	assert.Zero(t, record.Runs[0].WorkflowProtocolVersion)
	assert.NotContains(t, record.Content, parts.Request, "full request verification bytes are never exported as content")
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
}

func TestExportRejectsTamperedRequest(t *testing.T) {
	f := newRecordFixture()
	f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	record.Runs[0].Calls[0].Request.Model = "tampered"
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	_, err = runrecord.FromExport(data)
	assert.ErrorIs(t, err, callrecord.ErrRequestMismatch)
}

func TestReadRejectsMissingAndMalformedEvidence(t *testing.T) {
	failure := errors.New("read failure")
	for _, test := range []struct {
		name         string
		setup        func(recordFixture)
		storeFailure bool
	}{

		{name: "malformed call", setup: func(f recordFixture) {
			require.NoError(t, f.store.PutCall(t.Context(), "root", runstore.CallRecord{Key: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, State: runstore.CallStarted, Started: []byte("{")}))
		}},
		{name: "malformed event", setup: func(f recordFixture) {
			_, err := f.log.Append(t.Context(), &runlog.Event{RunID: "run", EventKey: "bad", Type: hooks.PlannerStep, Payload: []byte("{"), Timestamp: time.Unix(1, 0)})
			require.NoError(t, err)
		}},
		{name: "invalid segment", setup: func(f recordFixture) {
			head := f.put(t, map[string]string{"bad": "segment"})
			f.event(t, hooks.NewRunSealedEvent("run", head, recordedProtocolVersion, time.Unix(1, 0)))
		}},
		{name: "missing head", setup: func(f recordFixture) {
			f.event(t, hooks.NewRunSealedEvent("run", "sha256:missing", recordedProtocolVersion, time.Unix(1, 0)))
		}},
		{name: "adopted call absent", setup: func(f recordFixture) {
			f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{{RunID: "run", Seq: 1, Attempt: 1}}, recordedProtocolVersion, time.Unix(1, 0)))
		}},
		{name: "missing checkpoint", setup: func(f recordFixture) {
			h := runstore.Hash("sha256:missing")
			f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{Checkpoint: &h}, nil, recordedProtocolVersion, time.Unix(1, 0)))
		}},
		{name: "get error", storeFailure: true, setup: func(f recordFixture) {
			f.call(t, "run", 1, 1, runstore.CallStarted, callrecord.ActivityPlanner, "")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRecordFixture()
			test.setup(f)
			if test.storeFailure {
				f.store = failingReadStore{Store: f.store, failure: failure}
			}
			record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			require.Error(t, err)
			assert.Nil(t, record)
			if test.storeFailure {
				require.ErrorIs(t, err, failure)
			}
		})
	}
	_, err := runrecord.Read(t.Context(), nil, nil, "", "")
	require.Error(t, err)
	_, err = runrecord.FromExport([]byte(`{"version":2,"root_run_id":"root"}`))
	require.Error(t, err)
}

func TestEventsPaginateAndKeepLatestHead(t *testing.T) {
	f := newRecordFixture()
	for step := 1; step <= 105; step++ {
		f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", step, index.ViewRef{}, nil, recordedProtocolVersion, time.Unix(int64(step), 0)))
	}
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	assert.Len(t, record.Runs[0].Events, 105)
	assert.False(t, record.Runs[0].Sealed)
}

func TestReadOrphanCallViewAndCheckpoint(t *testing.T) {
	f := newRecordFixture()
	canonical := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: f.put(t, textMessage("canonical"))}})
	assistant := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "orphan answer"}}}
	orphan := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(t, textMessage("orphan prefix"))},
		{ID: 2, Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: f.put(t, assistant)},
		{ID: 3, Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(t, textMessage("orphan tail"))},
		{ID: 4, Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: f.put(t, assistant)},
	})
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) { return f.store.Get(ctx, "root", h) }
	entries, err := index.Walk(t.Context(), source, orphan)
	require.NoError(t, err)
	hash := f.put(t, index.CheckpointRecord{SummaryRun: "run", Through: 3, Chain: entries[2].Chain, Receipt: []index.ReceiptLine{{Kind: "assistant", From: 2, Through: 2, Text: "orphan answer"}}})
	view := index.ViewRef{IndexHead: orphan, Checkpoint: &hash, TailFrom: 4, Budget: index.RenderBudget{KeepUserTokens: 100, MaxSummaryTokens: 100, ReceiptTokens: 100, FramingTokens: 200}}
	messages, err := checkpoint.Project(t.Context(), source, entries, view)
	require.NoError(t, err)
	parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", &model.Request{Messages: messages}, view, messages)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	started, err := callrecord.Encode(callrecord.Started{Key: key, Activity: callrecord.ActivityPlanner, Step: 1, Request: parts})
	require.NoError(t, err)
	require.NoError(t, f.store.PutCall(t.Context(), "root", runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}))
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: canonical}, nil, recordedProtocolVersion, time.Unix(1, 0)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	assert.Equal(t, []*model.Message{textMessage("canonical")}, record.Runs[0].Transcript)
	assert.Equal(t, messages, record.Runs[0].Calls[0].Request.Messages)
	require.Len(t, record.Runs[0].Checkpoints, 1)
	assert.Equal(t, hash, record.Runs[0].Checkpoints[0].Hash)
}

func TestCheckpointCycleFails(t *testing.T) {
	f := newRecordFixture()
	hash := runstore.Hash("sha256:cycle")
	data, err := callrecord.Encode(index.CheckpointRecord{Prev: &hash})
	require.NoError(t, err)
	f.store = cyclicCheckpointStore{Store: f.store, hash: hash, data: data}
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{Checkpoint: &hash}, nil, recordedProtocolVersion, time.Unix(1, 0)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.ErrorContains(t, err, "cyclic checkpoint")
	assert.Nil(t, record)
}

func (s failingReadStore) Get(context.Context, string, runstore.Hash) ([]byte, error) {
	return nil, fmt.Errorf("fake store: %w", s.failure)
}

func (s cyclicCheckpointStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if hash == s.hash {
		return s.data, nil
	}
	return s.Store.Get(ctx, scope, hash)
}

func appendFakeSummaryOutput(t *testing.T, f recordFixture, keys []runstore.CallKey) {
	t.Helper()
	// This fake activity returns every chunk call, just as K15 will. Its output
	// is adopted by a canonical compaction event, not by MessageEntry.Call.
	output := struct{ CallKeys []runstore.CallKey }{CallKeys: keys}
	data, err := json.Marshal(output, json.Deterministic(true))
	require.NoError(t, err)
	_, err = f.log.Append(t.Context(), &runlog.Event{RunID: "run", EventKey: "summary:run:2", Type: hooks.ContextCompactionSkipped, Payload: data, Timestamp: time.Unix(2, 0)})
	require.NoError(t, err)
}
