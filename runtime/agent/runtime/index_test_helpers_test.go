package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/require"
)

// testAgentContext supplies the required activity-owned recorder for context fixtures.
func testAgentContext(opts agentContextOptions) planner.PlannerContext {
	if opts.recording == nil {
		opts.recording = NewCallRecording(opts.runtime.RunStore, opts.rootRunID, index.ViewRef{}, nil, 0)
	}
	return newAgentContext(opts)
}

func testSeedView(t *testing.T, rt *Runtime, messages []*model.Message, root string) index.ViewRef {
	t.Helper()
	if rt.RunStore == nil {
		rt.RunStore = storeinmem.New()
	}
	items, err := encodeContentMessages(messages)
	require.NoError(t, err)
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: root, Items: items, SeedMessages: true})
	require.NoError(t, err)
	return index.ViewRef{IndexHead: out.IndexHead}
}

func testWorkflowInput(t *testing.T, rt *Runtime, in *planner.PlanInput) *workflowPlanInput {
	t.Helper()
	if in.RunContext.RootRunID == "" {
		in.RunContext.RootRunID = "test-root"
	}
	view := testSeedView(t, rt, in.Messages, in.RunContext.RootRunID)
	entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, in.RunContext.RootRunID, hash)
	}, view.IndexHead)
	require.NoError(t, err)
	return &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, RunContext: in.RunContext, Entries: entries, IndexHead: view.IndexHead, Committed: len(entries), AgentID: "test.agent"}
}

func testStoredTranscript(t *testing.T, rt *Runtime, out *PlanActivityOutput, root string) []*model.Message {
	t.Helper()
	if out.TranscriptRef == "" {
		return nil
	}
	for key := range rt.viewCaches {
		root = strings.SplitN(key, "\x00", 2)[0]
		break
	}
	data, err := rt.RunStore.Get(t.Context(), root, out.TranscriptRef)
	require.NoError(t, err)
	var messages []*model.Message
	require.NoError(t, json.Unmarshal(data, &messages))
	return messages
}

func testView(t *testing.T, rt *Runtime, base *workflowPlanInput) *loadedView {
	t.Helper()
	view, err := rt.loadTestView(t.Context(), base.RunContext.RootRunID, base.RunContext.RunID, base.view())
	require.NoError(t, err)
	return view
}

func testIndexContext(t *testing.T, rt *Runtime) context.Context {
	t.Helper()
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt}
	return wf.Context()
}

func testRecordAssistant(t *testing.T, rt *Runtime, base *workflowPlanInput, calls []planner.ToolRequest) {
	t.Helper()
	out, err := rt.storePlanActivityOutput(t.Context(), base.RunContext.RootRunID, &PlanActivityOutput{Result: &planner.PlanResult{ToolCalls: calls}, IndexHead: base.IndexHead}, nil, nil)
	require.NoError(t, err)
	if base.CallRefs == nil {
		base.CallRefs = make(map[string]runstore.Hash)
	}
	for i, call := range calls {
		base.CallRefs[call.ToolCallID] = out.ToolCallRefs[i]
	}
	require.NoError(t, base.recordAssistant(out.TranscriptRef, calls))
}

func testRunLoop(t *testing.T, rt *Runtime, wf engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *workflowPlanInput, result *planner.PlanResult, messages []*model.Message, caps policy.CapsState, budget, hard time.Time, next int, turn string, ctrl *interrupt.Controller, initial ...workflowLoopStart) (*RunOutput, error) {
	t.Helper()
	if fake, ok := wf.(*testWorkflowContext); ok {
		fake.finishRuntime = rt
	}
	base.AgentID = input.AgentID
	if len(base.Entries) == 0 && input.RootRunID != "" {
		base.RunContext.RootRunID = input.RootRunID
	}
	req := &PlanActivityInput{AgentID: input.AgentID, RunContext: base.RunContext}
	rt.canonicalizePlanActivityToolCalls(result, req)
	out, err := rt.storePlanActivityOutput(t.Context(), base.RunContext.RootRunID, &PlanActivityOutput{Result: result, IndexHead: base.IndexHead}, messages, canonicalAwaitCalls(result))
	require.NoError(t, err)
	base.adopt(out)
	return rt.runLoop(wf, reg, input, base, result, out.TranscriptRef, model.TokenUsage{}, caps, budget, hard, next, turn, nil, ctrl, 0, initial...)
}

func testHideBookkeeping(t *testing.T, rt *Runtime, base *workflowPlanInput, results []*planner.ToolResult) {
	t.Helper()
	var ids []string
	for _, result := range results {
		if result != nil && result.Error == nil && rt.isBookkeeping(result.Name) {
			ids = append(ids, result.ToolCallID)
		}
	}
	require.NoError(t, base.structural(index.HideBookkeeping, ids))
}

func testPlannerView(t *testing.T, rt *Runtime, input *PlanActivityInput) *loadedView {
	t.Helper()
	out, err := rt.loadTestView(t.Context(), input.RunContext.RootRunID, input.RunID, input.View)
	require.NoError(t, err)
	return out
}

func (w *testWorkflowContext) storedPlannerOutput(ctx context.Context, input *PlanActivityInput, out *PlanActivityOutput) (*PlanActivityOutput, error) {
	rt := w.finishRuntime
	if rt == nil {
		rt = w.runtime
	}
	if rt == nil {
		rt = New()
	}
	root := input.RunContext.RootRunID
	if root == "" {
		root = "test-root"
	}
	view, err := rt.loadTestView(ctx, root, input.RunID, input.View)
	if err != nil {
		return nil, err
	}
	out.IndexHead = view.Head
	rt.canonicalizePlanActivityToolCalls(out.Result, input)
	var transcript []*model.Message
	if out.Result != nil && out.Result.FinalResponse != nil {
		transcript = []*model.Message{finalPlannerMessage(out)}
	}
	return rt.storePlanActivityOutput(ctx, root, out, transcript, canonicalAwaitCalls(out.Result))
}

func testOutputView(t *testing.T, rt *Runtime, outputs []*api.ToolCallOutput) index.ViewRef {
	t.Helper()
	const root = "root"
	if rt.RunStore == nil {
		rt.RunStore = storeinmem.New()
	}
	base := &workflowPlanInput{}
	for _, input := range outputs {
		require.NotNil(t, input)
		data, err := json.Marshal(input)
		require.NoError(t, err)
		var output planner.ToolOutput
		require.NoError(t, json.Unmarshal(data, &output))
		for _, part := range []struct {
			data []byte
			ref  *runstore.Hash
		}{{output.Result, &output.Refs.Result}, {output.ServerData, &output.Refs.ServerData}, {output.Payload, &output.Refs.Payload}} {
			if len(part.data) > 0 {
				*part.ref, err = rt.RunStore.Put(t.Context(), root, part.data)
				require.NoError(t, err)
			}
		}
		output.Result, output.ServerData, output.Payload = nil, nil, nil
		data, err = json.Marshal(output, json.Deterministic(true))
		require.NoError(t, err)
		hash, err := rt.RunStore.Put(t.Context(), root, data)
		require.NoError(t, err)
		// An output metadata record does not itself append a provider message.
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.PolicyDenial, ProtocolVersion: index.TemplateProtocolVersion}, Output: hash}))
	}
	return base.view()
}

func testLoadToolResult(t *testing.T, ctx context.Context, input *planner.PlanResumeInput) []byte {
	t.Helper()
	require.Empty(t, input.ToolOutputs[0].Result)
	data, err := input.Content.LoadToolResult(ctx, input.ToolOutputs[0].Refs)
	require.NoError(t, err)
	return data
}
func testLoadServerData(t *testing.T, ctx context.Context, input *planner.PlanResumeInput) []byte {
	t.Helper()
	require.Empty(t, input.ToolOutputs[0].ServerData)
	data, err := input.Content.LoadServerData(ctx, input.ToolOutputs[0].Refs)
	require.NoError(t, err)
	return data
}
func testLoadToolPayload(t *testing.T, ctx context.Context, input *planner.PlanResumeInput) []byte {
	t.Helper()
	require.Empty(t, input.ToolOutputs[0].Payload)
	data, err := input.Content.LoadToolPayload(ctx, input.ToolOutputs[0])
	require.NoError(t, err)
	return data
}

func testTypedSignal(t *testing.T, rt *Runtime, root string, answer *api.TypedInputAnswer) *api.TypedInputAnswer {
	t.Helper()
	hash, err := rt.RunStore.Put(t.Context(), root, answer.Payload)
	require.NoError(t, err)
	out := *answer
	out.PayloadRef, out.Payload = hash, nil
	return &out
}

func testTypedValue(t *testing.T, rt *Runtime, base *workflowPlanInput) []byte {
	t.Helper()
	view := testView(t, rt, base)
	require.Len(t, view.Typed, 1)
	require.Empty(t, view.Typed[0].Payload)
	data, err := rt.RunStore.Get(t.Context(), base.RunContext.RootRunID, view.Typed[0].PayloadRef)
	require.NoError(t, err)
	return data
}

func testStoredBytes(t *testing.T, rt *Runtime, root string, hash runstore.Hash) []byte {
	t.Helper()
	data, err := rt.RunStore.Get(t.Context(), root, hash)
	require.NoError(t, err)
	return data
}

func testRecovery(t *testing.T, rt *Runtime, root string, recovery *ModelRecovery) *ModelRecovery {
	t.Helper()
	out, err := rt.storePlanActivityOutput(t.Context(), root, &PlanActivityOutput{Recovery: recovery}, nil, nil)
	require.NoError(t, err)
	return out.Recovery
}

func testPutBytes(t *testing.T, rt *Runtime, root string, data []byte) runstore.Hash {
	t.Helper()
	if rt.RunStore == nil {
		rt.RunStore = storeinmem.New()
	}
	hash, err := rt.RunStore.Put(t.Context(), root, data)
	require.NoError(t, err)
	return hash
}

func testPutJSON(t *testing.T, rt *Runtime, root string, value any) runstore.Hash {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return testPutBytes(t, rt, root, data)
}

// testStoreCalls mirrors the planner's pre-dispatch write without adding a message.
func testStoreCalls(t *testing.T, rt *Runtime, base *workflowPlanInput, calls []planner.ToolRequest) {
	t.Helper()
	if base.CallRefs == nil {
		base.CallRefs = make(map[string]runstore.Hash)
	}
	for _, call := range calls {
		base.CallRefs[call.ToolCallID] = testPutJSON(t, rt, base.RunContext.RootRunID, call)
	}
}
