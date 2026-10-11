package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	rthints "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hookReadStore struct {
	runstore.Store
	reads []runstore.Hash
}

func TestToolResultHookReferences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		resultBytes int
		serverBytes int
		inline      bool
	}{
		{"oversized", 2 << 20, 64, false},
		{"small", 64, 64, true},
		{"duplicate fields exceed bound", 600_000, 64, false},
		{"oversized sidecar", 64, 2 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			result := map[string]string{"text": strings.Repeat("x", tc.resultBytes)}
			data, err := json.Marshal(result)
			require.NoError(t, err)
			server, err := json.Marshal(map[string]string{"private": strings.Repeat("s", tc.serverBytes)})
			require.NoError(t, err)
			refs := tools.ResultRefs{}
			refs.Result, err = rt.RunStore.Put(t.Context(), "root", data)
			require.NoError(t, err)
			refs.ServerData, err = rt.RunStore.Put(t.Context(), "root", server)
			require.NoError(t, err)
			refs.Payload, err = rt.RunStore.Put(t.Context(), "root", []byte(`{"executed":"private"}`))
			require.NoError(t, err)
			segment, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.ToolResult, Hash: refs.Result, Record: []runstore.Hash{refs.ServerData, refs.Payload}, Batch: 1, ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "original preview"}}}})
			require.NoError(t, err)
			_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", Pending: segment.Entries, EventKey: "run_sealed:run", Timestamp: time.Unix(1, 0)})
			require.NoError(t, err)
			evt := hooks.NewToolResultReceivedEvent("run", "agent", "", "tool", "call", "", result, data, server, "original preview", nil, 0, nil, nil, nil)
			evt.Refs = refs
			require.NoError(t, rt.publishHookErr(t.Context(), evt, "turn"))
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
			require.NoError(t, err)
			require.Len(t, page.Events, 2)
			payload := page.Events[1].Payload
			assert.LessOrEqual(t, len(payload), maxHookPayloadBytes)
			assert.NotContains(t, string(payload), "hook_payload_too_large")
			assert.NotContains(t, string(payload), `"truncated"`)
			decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: hooks.ToolResultReceived, RunID: "run", AgentID: "agent", Payload: payload})
			require.NoError(t, err)
			got := decoded.(*hooks.ToolResultReceivedEvent)
			assert.Equal(t, refs, got.Refs)
			assert.Equal(t, "original preview", got.ResultPreview)
			if tc.inline {
				assert.JSONEq(t, string(data), string(got.ResultJSON))
				assert.JSONEq(t, string(server), string(got.ServerData))
				assert.Equal(t, result["text"], got.Result.(map[string]any)["text"])
			} else {
				assert.Nil(t, got.Result)
				assert.Empty(t, got.ResultJSON)
				assert.Empty(t, got.ServerData)
			}
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Results, 1)
			assert.Equal(t, data, record.Runs[0].Results[0].Content)
			require.Len(t, record.Runs[0].ServerOnly, 2)
			assert.Equal(t, server, record.Runs[0].ServerOnly[0].Content)
			assert.NotEmpty(t, record.Runs[0].Transcript)
			exported, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(exported)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

func TestLargeToolResultRunRecord(t *testing.T) {
	for _, tc := range []struct {
		inline       bool
		interception string
	}{
		{false, "none"}, {true, "none"}, {false, "drop"}, {true, "drop"}, {false, "rewrite"}, {true, "rewrite"},
	} {
		t.Run(fmt.Sprintf("inline=%v/interception=%s", tc.inline, tc.interception), func(t *testing.T) {
			rt := New()
			intercepted := 0
			rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				if event, ok := input.Event.(*hooks.ToolResultReceivedEvent); ok && tc.interception != "none" {
					intercepted++
					if tc.interception == "drop" {
						return &BeforeEventDecision{Drop: true}, nil
					}
					copy := *event
					copy.Refs = tools.ResultRefs{Result: "missing-result", ServerData: "missing-server", Payload: "missing-payload"}
					return &BeforeEventDecision{Event: &copy}, nil
				}
				return nil, nil
			}}}
			result := map[string]string{"text": strings.Repeat("x", 2<<20)}
			data, err := json.Marshal(result)
			require.NoError(t, err)
			server := rawjson.Message(`{"private":true}`)
			spec := newAnyJSONSpec("batch.hook_refs_large", "batch")
			rthints.RegisterResultHint(spec.Name, template.Must(template.New("large").Parse("original large result preview")))
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "batch", Inline: tc.inline, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result, ServerData: server}), nil
			}}))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, nil
			}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return oracleFinal(), nil
			}})
			_, err = rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
			require.NoError(t, err)
			page, err := rt.RunEventStore.List(t.Context(), "root", "", 100)
			require.NoError(t, err)
			var got *hooks.ToolResultReceivedEvent
			for _, event := range page.Events {
				if event.Type == hooks.ToolResultReceived {
					decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: event.Type, RunID: event.RunID, Payload: event.Payload})
					require.NoError(t, err)
					got = decoded.(*hooks.ToolResultReceivedEvent)
					assert.NotContains(t, string(event.Payload), "hook_payload_too_large")
				}
			}
			if tc.interception == "drop" {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.NotEmpty(t, got.Refs.Result)
				assert.Equal(t, "original large result preview", got.ResultPreview)
				assert.Nil(t, got.Result)
				assert.Empty(t, got.ResultJSON)
				assert.Empty(t, got.ServerData)
			}
			if tc.interception != "none" {
				assert.Equal(t, 1, intercepted)
			}
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
			require.NoError(t, err)
			assert.True(t, record.Runs[0].Sealed)
			require.Len(t, record.Runs[0].Results, 1)
			assert.Equal(t, data, record.Runs[0].Results[0].Content)
			require.NotEmpty(t, record.Runs[0].ServerOnly)
			assert.Equal(t, []byte(server), record.Runs[0].ServerOnly[0].Content)
			exported, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(exported)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

func TestStoredToolContentBoundedLoads(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bytes     int
		wantReads int
		inline    bool
	}{
		{"known oversized", 2 << 20, 0, false},
		{"result envelope oversized", 600_000, 1, false},
		{"small", 64, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &hookReadStore{Store: storeinmem.New()}
			rt := New(WithRunStore(store))
			rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"tool": newAnyJSONSpec("tool", "svc.ts")}
			data, err := json.Marshal(map[string]string{"text": strings.Repeat("x", tc.bytes)})
			require.NoError(t, err)
			result, err := store.Put(t.Context(), "root", data)
			require.NoError(t, err)
			server, err := store.Put(t.Context(), "root", []byte(`{"private":true}`))
			require.NoError(t, err)
			refs := tools.ResultRefs{Result: result, ServerData: server}
			p := &api.PublishToolContent{AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run", TurnID: "turn"}, Call: planner.ToolRequest{Name: "tool", ToolCallID: "call"}, Result: &api.ToolEvent{Name: "tool", ToolCallID: "call", Refs: refs, ResultBytes: len(data), Preview: "stored preview"}}
			require.NoError(t, rt.publishStoredToolContent(t.Context(), "root", p))
			assert.Len(t, store.reads, tc.wantReads)
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: hooks.ToolResultReceived, RunID: "run", AgentID: "agent", Payload: page.Events[0].Payload})
			require.NoError(t, err)
			got := decoded.(*hooks.ToolResultReceivedEvent)
			assert.Equal(t, refs, got.Refs)
			assert.Equal(t, "stored preview", got.ResultPreview)
			assert.Equal(t, tc.inline, len(got.ResultJSON) > 0)
		})
	}
}

func TestOversizedToolResultFallbackPreview(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored=%t", stored), func(t *testing.T) {
			rt := New()
			data := rawjson.Message(`"` + strings.Repeat("x", 2<<20) + `"`)
			hash, err := rt.RunStore.Put(t.Context(), "root", data)
			require.NoError(t, err)
			refs := tools.ResultRefs{Result: hash}
			if stored {
				p := &api.PublishToolContent{AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run"}, Call: planner.ToolRequest{Name: "tool", ToolCallID: "call"}, Result: &api.ToolEvent{Name: "tool", ToolCallID: "call", Refs: refs, ResultBytes: len(data)}}
				require.NoError(t, rt.publishStoredToolContent(t.Context(), "root", p))
			} else {
				evt := hooks.NewToolResultReceivedEvent("run", "agent", "", "tool", "call", "", nil, data, nil, "", nil, 0, nil, nil, nil)
				evt.Refs = refs
				require.NoError(t, rt.publishHookErr(t.Context(), evt, "turn"))
			}
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: hooks.ToolResultReceived, RunID: "run", Payload: page.Events[0].Payload})
			require.NoError(t, err)
			got := decoded.(*hooks.ToolResultReceivedEvent)
			assert.Equal(t, "Result omitted from run hooks because payload exceeded limits.", got.ResultPreview)
			assert.Equal(t, refs, got.Refs)
			assert.Empty(t, got.ResultJSON)
			assert.LessOrEqual(t, len(page.Events[0].Payload), maxHookPayloadBytes)
		})
	}
}

func TestDeniedConfirmationHookCarriesStoredRefs(t *testing.T) {
	rt := New()
	rt.toolSpecs = map[tools.Ident]tools.ToolSpec{"tool": newAnyJSONSpec("tool", "svc.ts")}
	base := testWorkflowInput(t, rt, &planner.PlanInput{RunContext: run.Context{RootRunID: "root", RunID: "run"}})
	call := planner.ToolRequest{Name: "tool", ToolCallID: "call", RunID: "run", AgentID: "agent", Payload: rawjson.Message(`{}`)}
	testStoreCalls(t, rt, base, []planner.ToolRequest{call})
	state := newRunLoopState(nil, "", model.TokenUsage{}, policy.CapsState{}, 1, toolPolicyEnvelope{})
	results, err := rt.handleDeniedConfirmation(testIndexContext(t, rt), base, state, "turn", 0, confirmationAwait{call: call, plan: &confirmationPlan{DeniedResult: map[string]any{"denied": true}}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Refs.Result)
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	assert.Equal(t, hooks.ToolCallScheduled, page.Events[0].Type)
	assert.Equal(t, hooks.ToolResultReceived, page.Events[1].Type)
	decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: hooks.ToolResultReceived, RunID: "run", AgentID: "agent", Payload: page.Events[1].Payload})
	require.NoError(t, err)
	assert.Equal(t, results[0].Refs, decoded.(*hooks.ToolResultReceivedEvent).Refs)
}

func (s *hookReadStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.reads = append(s.reads, hash)
	return s.Store.Get(ctx, scope, hash)
}
