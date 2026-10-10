package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contentBatchEngine struct {
	engine.Engine
	mu      sync.Mutex
	records []*api.RecordContentInput
}

func (e *contentBatchEngine) RegisterRecordContentActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	return e.Engine.RegisterRecordContentActivity(ctx, name, opts, func(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
		e.mu.Lock()
		e.records = append(e.records, in)
		e.mu.Unlock()
		return fn(ctx, in)
	})
}

func TestToolResultBatchUsesOneContentActivity(t *testing.T) {
	for _, inline := range []bool{true, false} {
		t.Run(fmt.Sprintf("inline=%v", inline), func(t *testing.T) {
			eng := &contentBatchEngine{Engine: engineinmem.New()}
			store := &contentPathStore{Store: storeinmem.New()}
			rt := New(WithEngine(eng), WithRunStore(store))
			spec := newAnyJSONSpec("batch.result", "batch")
			spec.ResultReminder = strings.Repeat("reminder", 4096)
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "batch", Inline: inline, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]string{"id": call.ToolCallID}, ServerData: rawjson.Message(`{"private":true}`)}), nil
			}}))
			calls := make([]planner.ToolRequest, 50)
			for i := range calls {
				calls[i] = planner.ToolRequest{Name: spec.Name, ToolCallID: fmt.Sprintf("call-%d", i), Payload: rawjson.Message(`{"input":"unique-payload"}`)}
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: calls}, nil
			}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				assert.Len(t, in.ToolOutputs, 50)
				for _, output := range in.ToolOutputs {
					payload, err := in.Content.LoadToolPayload(ctx, output)
					require.NoError(t, err)
					assert.JSONEq(t, `{"input":"unique-payload"}`, string(payload))
					if inline {
						assert.NotEmpty(t, output.Call)
						assert.Empty(t, output.Refs.Payload)
					}
					data, err := in.Content.LoadToolResult(ctx, output.Refs)
					require.NoError(t, err)
					assert.JSONEq(t, fmt.Sprintf(`{"id":%q}`, output.ToolCallID), string(data))
				}
				return oracleFinal(), nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
			require.NoError(t, err)
			eng.mu.Lock()
			defer eng.mu.Unlock()
			assert.Len(t, eng.records, 1, "one result batch must schedule one content activity")
			require.Len(t, eng.records, 1)
			reminders := 0
			for _, item := range eng.records[0].Items {
				if string(item) == spec.ResultReminder {
					reminders++
				}
			}
			assert.Equal(t, 1, reminders, "large structural text is stored once in the result batch")
			if inline {
				for _, write := range store.snapshot() {
					assert.NotEqual(t, `{"input":"unique-payload"}`, string(write.data), "payload must be read from the already stored call")
				}
			}
		})
	}
}

func TestProvidedResultBatchUsesOneContentActivity(t *testing.T) {
	eng := &contentBatchEngine{Engine: engineinmem.New()}
	bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
	rt := New(WithEngine(eng), WithHooks(bus))
	spec := newAnyJSONSpec("batch.external", "batch")
	spec.ResultReminder = strings.Repeat("provided reminder", 2048)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "batch", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		return nil, fmt.Errorf("external tool must not execute locally")
	}}))
	items := make([]planner.AwaitToolItem, 50)
	for i := range items {
		items[i] = planner.AwaitToolItem{Name: spec.Name, ToolCallID: fmt.Sprintf("call-%d", i), Payload: rawjson.Message(`{"external":true}`)}
	}
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "external", Items: items}))}, nil
	}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		assert.Len(t, in.ToolOutputs, 50)
		for _, output := range in.ToolOutputs {
			data, err := in.Content.LoadToolResult(ctx, output.Refs)
			require.NoError(t, err)
			assert.JSONEq(t, `{"ok":true}`, string(data))
			payload, err := in.Content.LoadToolPayload(ctx, output)
			require.NoError(t, err)
			assert.JSONEq(t, `{"external":true}`, string(payload))
		}
		return oracleFinal(), nil
	}})
	runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
		if e, ok := event.(*hooks.AwaitExternalToolsEvent); ok {
			results := make([]*api.ProvidedToolResult, len(e.Items))
			for i, item := range e.Items {
				results[i] = &api.ProvidedToolResult{Name: item.ToolName, ToolCallID: item.ToolCallID, Result: rawjson.Message(`{"ok":true}`)}
			}
			return rt.ProvideToolResults(t.Context(), &api.ToolResultsSet{RunID: "root", ID: e.ID, Results: results})
		}
		return nil
	})
	eng.mu.Lock()
	defer eng.mu.Unlock()
	require.Len(t, eng.records, 1)
	assert.Len(t, eng.records[0].Provided.Results, 50)
}

func TestFailedContentBatchDoesNotAdoptEntries(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithRunStore(store))
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	call := planner.ToolRequest{Name: "batch.result", ToolCallID: "call"}
	testRecordAssistant(t, rt, base, []planner.ToolRequest{call})
	before := append([]index.MessageEntry(nil), base.Entries...)
	store.failAt = len(store.snapshot()) + 2
	result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError(strings.Repeat("error", 4096))}
	err := rt.appendIndexResults(testIndexContext(t, rt), base, []planner.ToolRequest{call}, []*planner.ToolResult{result}, false)
	require.Error(t, err)
	assert.Equal(t, before, base.Entries)
}

func TestContentBatchPreservesEngineContext(t *testing.T) {
	native := &testWorkflowContext{ctx: t.Context()}
	batch := &workflowContentBatch{}
	wrapped := contentWorkflowContext{WorkflowContext: native, batch: batch}
	assert.Same(t, native, engine.WorkflowContextFromContext(wrapped.Context()), "Temporal Await requires the native context identity")
	assert.Same(t, batch, contentBatchFromContext(wrapped.Context()))
	child, cancel := wrapped.WithCancel()
	defer cancel()
	assert.Same(t, batch, contentBatchFromContext(child.Context()))
	assert.NotSame(t, native, engine.WorkflowContextFromContext(child.Context()), "cancellation must retain the child engine scope")
}
