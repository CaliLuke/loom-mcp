package trajectory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	loginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	store runstore.Store
	log   runlog.Store
}

func newFixture() fixture {
	return fixture{store: storeinmem.New(), log: loginmem.New()}
}

func treeRecord(t *testing.T) *runrecord.RunRecord {
	t.Helper()
	f := newFixture()
	chosen := []model.ToolCall{{Name: "agent.child", ID: "a", Payload: rawjson.Message(`{"z":9007199254740993,"a":1}`)}, {Name: "agent.child", ID: "b", Payload: rawjson.Message(`{"a":1,"z":9007199254740993}`)}}
	f.call(t, 1, 1, runstore.CallFailed, &model.Response{}, "provider_failure", "")
	adopted := f.call(t, 1, 2, runstore.CallCompleted, &model.Response{ToolCalls: chosen}, "", "")
	f.call(t, 1, 3, runstore.CallStarted, nil, "", "")
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 1, index.ViewRef{}, []runstore.CallKey{adopted}, api.WorkflowProtocolVersion, time.Unix(1, 0)), 1)
	calls := []runstore.Hash{
		f.put(t, planner.ToolRequest{Name: "agent.child", ToolCallID: "a", ParentToolCallID: "parent", Payload: chosen[0].Payload}),
		f.put(t, planner.ToolRequest{Name: "agent.child", ToolCallID: "b", Payload: chosen[1].Payload}),
	}
	resultA := f.put(t, map[string]any{"answer": 1})
	resultB := f.put(t, map[string]any{"answer": 2})
	outA := f.put(t, planner.ToolOutput{Name: "agent.child", ToolCallID: "a", Refs: tools.ResultRefs{Result: resultA}, Telemetry: &telemetry.ToolTelemetry{DurationMs: 25}})
	outB := f.put(t, planner.ToolOutput{Name: "agent.child", ToolCallID: "b", Refs: tools.ResultRefs{Result: resultB, Payload: f.put(t, map[string]any{"injected": true})}, Error: planner.NewToolError("failed")})
	head := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.Assistant, Calls: calls, Call: &adopted},
		{ID: 2, Kind: index.ToolResult, Hash: resultB, Output: outB, ToolUseIDs: []string{"b"}, Batch: 2},
		{ID: 3, Kind: index.ToolResult, Hash: resultA, Output: outA, ToolUseIDs: []string{"a"}, Batch: 2},
	})
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}
	last := f.call(t, 2, 1, runstore.CallCompleted, &model.Response{Content: []model.Message{*final}}, "", head)
	f.event(t, hooks.NewChildRunLinkedEvent("run", "agent", "session", "agent.child", "b", "child-b", "child"), 2)
	f.event(t, hooks.NewChildRunLinkedEvent("run", "agent", "session", "agent.child", "a", "child-a", "child"), 3)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 2, index.ViewRef{IndexHead: head}, []runstore.CallKey{last}, api.WorkflowProtocolVersion, time.Unix(4, 0)), 4)
	f.event(t, hooks.NewPlannerNoteEvent("run", "agent", "session", "note", nil), 5)
	sealed := f.segment(t, head, []index.MessageEntry{{ID: 4, Kind: index.Assistant, Hash: f.put(t, []*model.Message{final}), Call: &last}})
	f.event(t, hooks.NewRunSealedEvent("run", sealed, api.WorkflowProtocolVersion, time.Unix(6, 0)), 6)
	f.event(t, hooks.NewRunCompletedEvent("run", "agent", "session", "success", run.PhaseCompleted, nil), 7)
	for _, id := range []string{"child-b", "child-a"} {
		f.event(t, hooks.NewRunSealedEvent(id, "", api.WorkflowProtocolVersion, time.Unix(2, 0)), 2)
	}
	rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	return rec
}

func (f fixture) put(t *testing.T, value any) runstore.Hash {
	t.Helper()
	data, err := callrecord.Encode(value)
	require.NoError(t, err)
	return f.putBytes(t, data)
}

func (f fixture) putBytes(t *testing.T, data []byte) runstore.Hash {
	t.Helper()
	hash, err := f.store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	return hash
}

func (f fixture) segment(t *testing.T, prev runstore.Hash, entries []index.MessageEntry) runstore.Hash {
	t.Helper()
	var chain runstore.Hash
	if prev != "" {
		old, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
			return f.store.Get(ctx, "root", hash)
		}, prev)
		require.NoError(t, err)
		chain = old[len(old)-1].Chain
	}
	seg, err := index.NewSegment(prev, chain, entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	return f.putBytes(t, data)
}

func (f fixture) call(t *testing.T, step, attempt int, state runstore.CallState, response *model.Response, failure string, head runstore.Hash) runstore.CallKey {
	t.Helper()
	const id = "run"
	seq, err := f.store.NextSeq(t.Context(), "root", id)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: id, Seq: seq, Attempt: attempt}
	req := &model.Request{PromptRefs: []prompt.PromptRef{{ID: "system", Version: "v1"}}, Model: "provider", Messages: []*model.Message{}, Tools: []*model.ToolDefinition{}}
	parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", req, index.ViewRef{IndexHead: head}, nil)
	require.NoError(t, err)
	started, err := callrecord.Encode(callrecord.Started{Key: key, Step: step, Activity: callrecord.ActivityPlanner, Model: "provider", ModelID: "registered", Prompts: req.PromptRefs, Request: parts})
	require.NoError(t, err)
	record := runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}
	require.NoError(t, f.store.PutCall(t.Context(), "root", record))
	if state != runstore.CallStarted {
		hash := f.put(t, response)
		completion := callrecord.Completion{Response: &hash, Usage: model.TokenUsage{Model: "resolved", InputTokens: 2, OutputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5, TotalTokens: 14}, Latency: 12 * time.Millisecond, Facts: &contextmeasure.RequestFacts{Call: key, Model: "resolved"}}
		if failure != "" {
			completion.Error = &callrecord.CallError{Kind: failure, Message: "attempt failed"}
		}
		record.State = state
		record.Completion, err = callrecord.Encode(completion)
		require.NoError(t, err)
		require.NoError(t, f.store.PutCall(t.Context(), "root", record))
	}
	return key
}

func (f fixture) event(t *testing.T, event hooks.Event, seconds int64) {
	t.Helper()
	input, err := hooks.EncodeToHookInput(event, "")
	require.NoError(t, err)
	// Fixture identities are repeatable across independently populated stores.
	input.EventKey = fmt.Sprintf("%s:%s:%d", event.RunID(), event.Type(), seconds)
	_, err = f.log.Append(t.Context(), &runlog.Event{RunID: event.RunID(), AgentID: agent.Ident(event.AgentID()), SessionID: "session", EventKey: input.EventKey, Type: event.Type(), Payload: input.Payload, Timestamp: time.Unix(seconds, 0)})
	require.NoError(t, err)
}
