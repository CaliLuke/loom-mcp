package runrecord_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	loginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordFixture struct {
	store runstore.Store
	log   runlog.Store
}

const recordedProtocolVersion = 17

func TestReadClassifiesAttemptsAndPresentations(t *testing.T) {
	f := newRecordFixture()
	head := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: f.put(t, textMessage("seed"))}})
	keys := []runstore.CallKey{f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, head), f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, head)}
	abandoned := f.call(t, "run", 2, 1, runstore.CallFailed, callrecord.ActivityPlanner, head)
	incomplete := f.call(t, "run", 2, 2, runstore.CallStarted, callrecord.ActivityPlanner, head)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, keys, recordedProtocolVersion, time.Unix(1, 0)))
	p := hooks.NewAssistantPresentationCommittedEvent("run", "agent", "", []string{"adopted", "lost", "standalone"}, []*model.Message{textMessage("shown")})
	p.CallKeys = []*runstore.CallKey{&keys[0], &abandoned, nil}
	f.event(t, p)
	sealedHead := f.segment(t, head, []index.MessageEntry{{ID: 2, Kind: index.Assistant, Hash: f.put(t, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}}), Call: &keys[0]}})
	f.event(t, hooks.NewRunSealedEvent("run", sealedHead, recordedProtocolVersion, time.Unix(100, 123)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	r := record.Runs[0]
	assert.True(t, r.Sealed)
	assert.Equal(t, sealedHead, r.IndexHead)
	assert.Equal(t, recordedProtocolVersion, r.WorkflowProtocolVersion)
	require.Len(t, r.Calls, 4)
	assert.Equal(t, []runrecord.CallClass{runrecord.Adopted, runrecord.Adopted, runrecord.Abandoned, runrecord.Incomplete}, []runrecord.CallClass{r.Calls[0].Class, r.Calls[1].Class, r.Calls[2].Class, r.Calls[3].Class})
	assert.Equal(t, incomplete, r.Calls[3].Record.Key)
	assert.Equal(t, 2, r.Calls[2].Step)
	assert.Equal(t, callrecord.ActivityPlanner, r.Calls[3].Activity)
	assert.Equal(t, []runrecord.PresentationClass{runrecord.PresentationAdopted, runrecord.PresentedNotAdopted, runrecord.PresentedUnrecorded}, []runrecord.PresentationClass{r.Presentations[0].Class, r.Presentations[1].Class, r.Presentations[2].Class})
	assert.Equal(t, []*model.Message{textMessage("seed")}, r.Calls[0].Request.Messages)
	assert.Len(t, r.Transcript, 2)
	bytes, err := record.Export().Marshal()
	require.NoError(t, err)
	again, err := record.Export().Marshal()
	require.NoError(t, err)
	assert.Equal(t, bytes, again)
	restored, err := runrecord.FromExport(bytes)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
}

func TestReadTreeAndFullContent(t *testing.T) {
	f := newRecordFixture()
	eligible := f.put(t, map[string]any{"full": "result"})
	secret := f.put(t, map[string]any{"secret": "server only"})
	typed := f.put(t, map[string]any{"answer": true})
	clarify, err := f.store.Put(t.Context(), "root", []byte("await answer"))
	require.NoError(t, err)
	head := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.TypedInput, Hash: typed, InputID: "typed"},
		{ID: 2, Kind: index.Clarification, Hash: clarify},
		{ID: 3, Kind: index.ToolResult, Hash: eligible, Record: []runstore.Hash{secret}, ToolUseIDs: []string{"call"}, Batch: 3, Bytes: 2 << 20, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "preview"}}},
		{ID: 4, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.Reminders, ProtocolVersion: 5, Text: []index.TextParameter{{Text: "old template"}}}},
	})
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, nil, recordedProtocolVersion, time.Unix(1, 0)))
	f.event(t, hooks.NewChildRunLinkedEvent("run", "agent", "", "tool", "call", "child", "child-agent"))
	childKey := f.call(t, "child", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	f.event(t, hooks.NewPlannerStepEvent("child", "child-agent", "", 1, index.ViewRef{}, []runstore.CallKey{childKey}, recordedProtocolVersion, time.Unix(1, 0)))
	f.event(t, hooks.NewRunSealedEvent("child", "", recordedProtocolVersion, time.Unix(2, 0)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs, 2)
	assert.False(t, record.Runs[0].Sealed)
	assert.Equal(t, "child", record.Runs[1].RunID)
	assert.Len(t, record.Runs[0].TypedAnswers, 1)
	assert.Equal(t, "await answer", string(record.Runs[0].AwaitAnswers[0].Content))
	assert.JSONEq(t, `{"full":"result"}`, string(record.Runs[0].Results[0].Content))
	assert.JSONEq(t, `{"secret":"server only"}`, string(record.Runs[0].ServerOnly[0].Content))
	assert.Equal(t, record.Runs[0].ServerOnly[0].Content, record.Content[secret])
	bytes, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(bytes)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
}

func newRecordFixture() recordFixture {
	return recordFixture{store: storeinmem.New(), log: loginmem.New()}
}

func textMessage(text string) *model.Message {
	return &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}
}

func (f recordFixture) put(t *testing.T, value any) runstore.Hash {
	t.Helper()
	data, err := callrecord.Encode(value)
	require.NoError(t, err)
	hash, err := f.store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	return hash
}

func (f recordFixture) segment(t *testing.T, prev runstore.Hash, entries []index.MessageEntry) runstore.Hash {
	t.Helper()
	var chain runstore.Hash
	if prev != "" {
		old, err := index.Walk(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return f.store.Get(ctx, "root", h)
		}, prev)
		require.NoError(t, err)
		chain = old[len(old)-1].Chain
	}
	seg, err := index.NewSegment(prev, chain, entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	hash, err := f.store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	return hash
}

func (f recordFixture) call(t *testing.T, run string, step, attempt int, state runstore.CallState, activity callrecord.ActivityKind, head runstore.Hash) runstore.CallKey {
	t.Helper()
	seq, err := f.store.NextSeq(t.Context(), "root", run)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: run, Seq: seq, Attempt: attempt}
	view, err := index.Walk(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return f.store.Get(ctx, "root", h)
	}, head)
	require.NoError(t, err)
	messages, err := index.Render(t.Context(), view, func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return f.store.Get(ctx, "root", h)
	})
	require.NoError(t, err)
	req := &model.Request{Model: "fake", Messages: messages}
	parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", req, index.ViewRef{IndexHead: head}, messages)
	require.NoError(t, err)
	started, err := callrecord.Encode(callrecord.Started{Key: key, Step: step, Activity: activity, Model: "fake", Request: parts})
	require.NoError(t, err)
	r := runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}
	require.NoError(t, f.store.PutCall(t.Context(), "root", r))
	if state != runstore.CallStarted {
		r.State = state
		r.Completion, err = callrecord.Encode(callrecord.Completion{})
		require.NoError(t, err)
		require.NoError(t, f.store.PutCall(t.Context(), "root", r))
	}
	return key
}

func (f recordFixture) event(t *testing.T, e hooks.Event) {
	t.Helper()
	input, err := hooks.EncodeToHookInput(e, "")
	require.NoError(t, err)
	_, err = f.log.Append(t.Context(), &runlog.Event{RunID: e.RunID(), EventKey: input.EventKey, Type: e.Type(), Payload: input.Payload, Timestamp: time.UnixMilli(input.TimestampMS).UTC()})
	require.NoError(t, err)
}
