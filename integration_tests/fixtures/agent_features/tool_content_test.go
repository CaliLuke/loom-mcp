package agentfeatures_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"

	"example.com/agentfeatures/gen/features"
	"example.com/agentfeatures/gen/features/agents/coordinator"
	agentworkflow "example.com/agentfeatures/gen/features/agents/coordinator/workflow"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	runstoreinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contentWrite struct {
	scope string
	hash  runstore.Hash
}
type recordingContentStore struct {
	runstore.Store
	mu     sync.Mutex
	writes []contentWrite
	failAt int
}
type contentPlanner struct {
	t     *testing.T
	store *recordingContentStore
	fail  bool
}

func TestActivityContentTypedLargeRoundTrip(t *testing.T) {
	store := &recordingContentStore{Store: runstoreinmem.New()}
	fx := newFeatureRuntime(t, agentsruntime.WithRunStore(store))
	original := &workflow.MethodEchoResult{OK: true, Message: strings.Repeat("a", 5*1024*1024)}
	exec := agentworkflow.NewCoordinatorWorkflowExec(agentworkflow.WithMethodEcho(func(context.Context, any) (any, error) {
		return &features.MethodEchoResult{OK: original.OK, Message: original.Message}, nil
	}))
	registerCoordinatorToolsets(t, t.Context(), fx.rt, exec)
	out, err := fx.rt.ExecuteToolActivity(t.Context(), &api.ToolInput{
		RunID: "child", RootRunID: "root", SessionID: "private-session", ToolName: workflow.MethodEcho,
		ToolCallID: "large", Payload: rawjson.Message(`{"topic":"large"}`),
	})
	require.NoError(t, err)
	spec, ok := workflow.Spec(workflow.MethodEcho)
	require.True(t, ok)
	decoded, err := agentsruntime.LoadToolResult(t.Context(), store, "root", *spec, out.Refs.Result)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
	canonical, err := workflow.MethodEchoResultCodec.ToJSON(original)
	require.NoError(t, err)
	stored, err := store.Get(t.Context(), "root", out.Refs.Result)
	require.NoError(t, err)
	assert.Equal(t, canonical, stored)
	assert.Equal(t, canonical, []byte(out.Payload))
	assertStoredRefs(t, store, "root", out.Refs)
	_, err = agentsruntime.LoadToolResult(t.Context(), store, "child", *spec, out.Refs.Result)
	assert.ErrorIs(t, err, runstore.ErrNotFound)
	_, err = agentsruntime.LoadToolResult(t.Context(), store, "root", *spec, "")
	assert.ErrorIs(t, err, runstore.ErrNotFound)
}

func TestActivityContentGeneratedDispatchAndWorkflow(t *testing.T) {
	for _, failAt := range []int{0, 1, 2, 3} {
		t.Run([]string{"success", "result put fails", "server data put fails", "payload put fails"}[failAt], func(t *testing.T) {
			store := &recordingContentStore{Store: runstoreinmem.New(), failAt: failAt}
			before := agentsruntime.RuntimeInterceptorFuncs{BeforeToolFunc: func(_ context.Context, input *agentsruntime.BeforeToolInput) (*agentsruntime.BeforeToolDecision, error) {
				assert.JSONEq(t, `{"topic":"adapter"}`, string(input.Call.Payload))
				return &agentsruntime.BeforeToolDecision{Payload: rawjson.Message(`{"topic":"interceptor"}`)}, nil
			}}
			fx := newFeatureRuntime(t, agentsruntime.WithRunStore(store), agentsruntime.WithInterceptors(before))
			exec := agentworkflow.NewCoordinatorWorkflowExec(
				agentworkflow.WithPayloadMapper(func(_ tools.Ident, value any, _ *agentsruntime.ToolCallMeta) (any, error) {
					args := value.(*workflow.MethodEchoPayload)
					assert.Equal(t, "interceptor", args.Topic)
					return &features.MethodEchoPayload{Topic: "mapped"}, nil
				}),
				agentworkflow.WithInterceptors(agentworkflow.ToolInterceptorFunc(func(_ context.Context, value any, _ *agentsruntime.ToolCallMeta) error {
					payload := value.(*features.MethodEchoPayload)
					assert.Equal(t, "private-session", payload.SessionID)
					payload.Topic += "-injected"
					return nil
				})),
				agentworkflow.WithMethodEcho(func(_ context.Context, value any) (any, error) {
					payload := value.(*features.MethodEchoPayload)
					assert.Equal(t, "mapped-injected", payload.Topic)
					assert.Equal(t, "private-session", payload.SessionID)
					// A later service mutation must not alter the recorded input snapshot.
					payload.Topic = "mutated-by-service"
					return &features.MethodEchoResult{OK: true, Message: "eligible"}, nil
				}),
			)
			reg := agentsruntime.ToolsetRegistration{
				Name: "features.workflow", Specs: workflow.Specs, DecodeInExecutor: true,
				Execute: func(ctx context.Context, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
					return exec.Execute(ctx, &agentsruntime.ToolCallMeta{RunID: call.RunID, SessionID: call.SessionID, ToolCallID: call.ToolCallID}, call)
				},
			}
			reg.PayloadAdapter = func(context.Context, agentsruntime.ToolCallMeta, tools.Ident, jsontext.Value) (jsontext.Value, error) {
				return jsontext.Value(`{"topic":"adapter"}`), nil
			}
			reg.ResultMaterializer = func(_ context.Context, _ agentsruntime.ToolCallMeta, _ *planner.ToolRequest, result *planner.ToolResult) error {
				result.ServerData = rawjson.Message(`{"private":"sidecar-marker"}`)
				return nil
			}
			require.NoError(t, fx.rt.RegisterToolset(reg))
			require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{
				Planner: contentPlanner{t: t, store: store, fail: failAt != 0},
			}))
			_, err := fx.rt.CreateSession(t.Context(), "private-session")
			require.NoError(t, err)
			out, err := coordinator.NewClient(fx.rt).Run(t.Context(), "private-session", nil, agentsruntime.WithRunID("content-root"), func(input *api.RunInput) {
				input.RootRunID = "caller-override"
			})
			require.NoError(t, err)
			require.Len(t, out.ToolEvents, 1)
			event := out.ToolEvents[0]
			if failAt != 0 {
				assert.Empty(t, event.Refs)
				assert.NotNil(t, event.Error)
				return
			}
			assertStoredRefs(t, store, "content-root", event.Refs)
			for _, part := range []struct {
				hash runstore.Hash
				want string
			}{
				{event.Refs.Result, `{"ok":true,"message":"eligible"}`},
				{event.Refs.ServerData, `{"private":"sidecar-marker"}`},
				{event.Refs.Payload, `{"topic":"mapped-injected","session_id":"private-session"}`},
			} {
				data, err := store.Get(t.Context(), "content-root", part.hash)
				require.NoError(t, err)
				assert.JSONEq(t, part.want, string(data))
			}
			assert.JSONEq(t, `{"ok":true,"message":"eligible"}`, string(event.Result))
			assert.NotEmpty(t, event.ServerData)
		})
	}
}

func (s *recordingContentStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt != 0 && len(s.writes)+1 == s.failAt {
		return "", runstore.ErrContentTooLarge
	}
	hash, err := s.Store.Put(ctx, scope, data)
	if err == nil {
		s.writes = append(s.writes, contentWrite{scope: scope, hash: hash})
	}
	return hash, err
}

func (p contentPlanner) PlanStart(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	assert.Equal(p.t, "content-root", input.RunContext.RootRunID)
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: workflow.MethodEcho, ToolCallID: "echo", Payload: rawjson.Message(`{"topic":"model"}`)}}}, nil
}

func (p contentPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	require.Len(p.t, input.ToolOutputs, 1)
	output := input.ToolOutputs[0]
	if p.fail {
		assert.Empty(p.t, output.Refs)
		assert.NotNil(p.t, output.Error)
	} else {
		assertStoredRefs(p.t, p.store, "content-root", output.Refs)
		assert.JSONEq(p.t, `{"topic":"model"}`, string(output.Payload))
		messages, err := json.Marshal(input.Messages)
		require.NoError(p.t, err)
		for _, secret := range []string{"private-session", "sidecar-marker", string(output.Refs.ServerData), string(output.Refs.Payload)} {
			assert.NotContains(p.t, string(messages), secret)
		}
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}},
	}}}, nil
}

func assertStoredRefs(t *testing.T, store *recordingContentStore, scope string, refs tools.ResultRefs) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	require.NotEmpty(t, refs.Result)
	require.NotEmpty(t, refs.Payload)
	for _, hash := range []runstore.Hash{refs.Result, refs.ServerData, refs.Payload} {
		if hash == "" {
			continue
		}
		assert.Contains(t, store.writes, contentWrite{scope: scope, hash: hash})
	}
}
