package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type indexCaptureEngine struct {
	engine.Engine
	inputs  []*api.PlanActivityInput
	heads   []runstore.Hash
	outputs []*api.PlanActivityOutput
}

func (e *indexCaptureEngine) RegisterPlannerActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error)) error {
	return e.Engine.RegisterPlannerActivity(ctx, name, opts, func(ctx context.Context, in *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		var copy api.PlanActivityInput
		if err := json.Unmarshal(data, &copy); err != nil {
			return nil, err
		}
		e.inputs = append(e.inputs, &copy)
		out, err := fn(ctx, in)
		if out != nil {
			e.heads = append(e.heads, out.IndexHead)
			e.outputs = append(e.outputs, out)
		}
		return out, err
	})
}

func TestRuntimeIndexReconstructsAndBoundsPlannerInput(t *testing.T) {
	eng := &indexCaptureEngine{Engine: engineinmem.New()}
	rt := New(WithEngine(eng))
	spec := newAnyJSONSpec("indexed.large", "indexed")
	const steps = 24
	payload := strings.Repeat("result-content", 8000)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "indexed", Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: payload}), nil
	}}))
	step := 0
	next := func() *planner.PlanResult {
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, Payload: rawjson.Message(`{}`)}}}
	}
	registerContentAgent(t, rt, &stubPlanner{start: func(_ context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		assert.Equal(t, canonicalIndexMessages(t, []*model.Message{contentMessage("seed")}), canonicalIndexMessages(t, in.Messages))
		return next(), nil
	}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		step++
		require.Len(t, in.ToolOutputs, step)
		latest := in.ToolOutputs[step-1]
		assert.Empty(t, latest.Result)
		full, err := in.Content.LoadToolResult(ctx, latest.Refs)
		require.NoError(t, err)
		assert.JSONEq(t, fmt.Sprintf("%q", payload), string(full))
		if step == steps {
			return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
		}
		return next(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("seed")}, WithRunID("root"))
	require.NoError(t, err)
	require.Len(t, eng.inputs, steps+1)
	require.NotEmpty(t, eng.inputs[0].View.IndexHead)
	require.Empty(t, eng.inputs[0].View.Pending, "seed never enters pending")
	var committed []index.MessageEntry
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", hash)
	}
	committed, err = index.Walk(t.Context(), source, eng.inputs[0].View.IndexHead)
	require.NoError(t, err)
	for i, in := range eng.inputs {
		if i > 0 {
			assert.Len(t, in.View.Pending, 2, "no accumulated history or unflushed pending entries")
			assert.Equal(t, eng.heads[i-1], in.View.IndexHead)
		}
		data, err := json.Marshal(in)
		require.NoError(t, err)
		assert.Less(t, len(data), 4096+400*len(in.View.Pending)+100, "whole activity input stays bounded at step %d", i)
		assert.NotContains(t, string(data), "result-content")
		committed = append(committed, in.View.Pending...)
		walked, err := index.Walk(t.Context(), source, eng.heads[i])
		require.NoError(t, err)
		assert.Equal(t, committed, walked, "head reconstructs exactly committed entries")
	}
}

func TestViewCommitRetryAndImmutableBookkeepingSegment(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	call := planner.ToolRequest{Name: "control.progress", ToolCallID: "hidden"}
	testRecordAssistant(t, rt, base, []planner.ToolRequest{call})
	original := base.view()
	first, err := rt.loadView(t.Context(), base.RunContext.RootRunID, "run", original)
	require.NoError(t, err)
	// A crashed worker has not returned the new head. Retry the same pending input.
	cold := New(WithRunStore(store))
	retry, err := cold.loadView(t.Context(), base.RunContext.RootRunID, "run", original)
	require.NoError(t, err)
	assert.Equal(t, first.Head, retry.Head)
	writes := store.snapshot()
	require.GreaterOrEqual(t, len(writes), 3)
	assert.Equal(t, writes[len(writes)-2].data, writes[len(writes)-1].data)
	before := testStoredBytes(t, rt, base.RunContext.RootRunID, first.Head)
	base.adopt(&PlanActivityOutput{IndexHead: first.Head})
	require.NoError(t, base.structural(index.HideBookkeeping, []string{"hidden"}))
	hidden, err := rt.loadView(t.Context(), base.RunContext.RootRunID, "run", base.view())
	require.NoError(t, err)
	assert.Empty(t, hidden.Messages)
	assert.Equal(t, before, testStoredBytes(t, rt, base.RunContext.RootRunID, first.Head))
	assert.NotEqual(t, first.Head, hidden.Head)
}

func TestViewCannotPublishHeadBeforeStoreOrWalkWrongPredecessor(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	require.NoError(t, base.structural(index.PolicyDenial, nil))
	store.failAt = store.attempts + 1
	out, err := rt.loadView(t.Context(), base.RunContext.RootRunID, "run", base.view())
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	assert.Nil(t, out)
	assert.Empty(t, base.IndexHead)
	store.failAt = 0
	first, err := rt.loadView(t.Context(), base.RunContext.RootRunID, "run", base.view())
	require.NoError(t, err)
	base.adopt(&PlanActivityOutput{IndexHead: first.Head})
	require.NoError(t, base.structural(index.PolicyDenial, nil))
	bad := base.view()
	bad.IndexHead = ""
	_, err = rt.loadView(t.Context(), base.RunContext.RootRunID, "wrong", bad)
	require.Error(t, err, "wrong prev cannot reconstruct a noninitial ID")
	_, err = rt.loadView(t.Context(), base.RunContext.RootRunID, "missing", index.ViewRef{IndexHead: "sha256:missing"})
	require.ErrorIs(t, err, runstore.ErrNotFound)
}

func TestIndexSegmentContentCapSplitsWithoutLosingEntries(t *testing.T) {
	store := storeinmem.New()
	entries := make([]index.MessageEntry, 1000)
	text := strings.Repeat("x", index.MaxInlineTextBytes)
	for i := range entries {
		entries[i] = index.MessageEntry{ID: index.MessageID(i + 1), Kind: index.Structural, Batch: 1, ToolUseIDs: []string{fmt.Sprint(i)}, Structural: &index.StructuralEntry{Kind: index.SynthesizedError, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Text: text}}}}
	}
	head, committed, err := storeIndexSegments(t.Context(), store, "root", "", "", entries)
	require.NoError(t, err)
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return store.Get(ctx, "root", hash)
	}
	walked, err := index.Walk(t.Context(), source, head)
	require.NoError(t, err)
	assert.Equal(t, committed, walked)
	segments := 0
	for head != "" {
		data, err := source(t.Context(), head)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(data), indexSegmentMaxBytes)
		var segment index.IndexSegment
		require.NoError(t, json.Unmarshal(data, &segment))
		segments++
		head = ""
		if segment.Prev != nil {
			head = *segment.Prev
		}
	}
	assert.Greater(t, segments, 1)
	rt := New(WithRunStore(store))
	// A split batch must still render as one user message on a cold worker.
	last, _, err := storeIndexSegments(t.Context(), store, "root", "", "", entries)
	require.NoError(t, err)
	view, err := rt.loadView(t.Context(), "root", "run", index.ViewRef{IndexHead: last})
	require.NoError(t, err)
	require.Len(t, view.Messages, 1)
	assert.Len(t, view.Messages[0].Parts, len(entries))
}

type indexReadStore struct {
	runstore.Store
	reads int
}

func (s *indexReadStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.reads++
	return s.Store.Get(ctx, scope, hash)
}

func TestWarmViewAppendsOnlyNewReferenceProjections(t *testing.T) {
	store := &indexReadStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	view := testOutputView(t, rt, []*api.ToolCallOutput{{ToolCallID: "first", Result: rawjson.Message(`{"value":1}`)}})
	first, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	reads := store.reads
	same, err := rt.loadView(t.Context(), "root", "run", index.ViewRef{IndexHead: first.Head})
	require.NoError(t, err)
	assert.Equal(t, reads, store.reads, "adopted warm head reads no old content")
	assert.Same(t, first.Outputs[0], same.Outputs[0], "metadata is derived once per worker")
	entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return store.Get(ctx, "root", hash)
	}, first.Head)
	require.NoError(t, err)
	base := &workflowPlanInput{Entries: entries, IndexHead: first.Head, Committed: len(entries)}
	hash := testPutBytes(t, rt, "root", []byte(`{"answer":true}`))
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.TypedInput, InputID: "answer", Hash: hash}))
	reads = store.reads
	next, err := rt.loadView(t.Context(), "root", "run", base.view())
	require.NoError(t, err)
	assert.Equal(t, 2, store.reads-reads, "one new segment and its typed answer")
	assert.Same(t, first.Outputs[0], next.Outputs[0])
	assert.Equal(t, []planner.TypedInputOutput{{ID: "answer", PayloadRef: hash}}, next.Typed)
	// Pending chains bind their exact predecessor, even when entry IDs fit.
	corrupted := base.view()
	corrupted.Pending[0].Chain = first.Head
	_, err = rt.loadView(t.Context(), "root", "run", corrupted)
	require.ErrorContains(t, err, "pending index chain mismatch")
}
