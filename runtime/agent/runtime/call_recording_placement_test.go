package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallRecordingInterceptorBoundary(t *testing.T) {
	for _, short := range []bool{false, true} {
		rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
			client, ok := agent.ModelClient("registered-model")
			require.True(t, ok)
			_, err := client.Complete(ctx, &model.Request{Model: "original"})
			return err
		})
		response := &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}
		raw := &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: response}
		rt.models["registered-model"] = raw
		rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
			if short {
				return &BeforeModelDecision{Response: response}, nil
			}
			req := *input.Request
			req.Model = "rewritten"
			req.Cache = &model.CacheOptions{AfterSystem: true}
			return &BeforeModelDecision{Request: &req}, nil
		}}}
		ctx := engine.WithActivityAttempt(t.Context(), 1)
		_, err := rt.PlanStartActivity(ctx, input)
		require.NoError(t, err)
		calls, err := rt.RunStore.ListCalls(ctx, "root-run", "child-run")
		require.NoError(t, err)
		if short {
			assert.Empty(t, calls)
			assert.Zero(t, raw.sent)
		} else {
			require.Len(t, calls, 1)
			started, err := callrecord.DecodeStarted(calls[0].Started)
			require.NoError(t, err)
			assert.Equal(t, "rewritten", started.Model)
			rebuilt, err := callrecord.Rebuild(ctx, rt.RunStore, "root-run", started, nil)
			require.NoError(t, err)
			assert.True(t, rebuilt.Cache.AfterSystem)
		}
		direct, ok := rt.ModelClient("registered-model")
		require.True(t, ok)
		_, err = direct.Complete(ctx, &model.Request{})
		require.NoError(t, err)
		after, err := rt.RunStore.ListCalls(ctx, "root-run", "child-run")
		require.NoError(t, err)
		assert.Equal(t, calls, after)
	}
}

func TestCallRecordingUsesPrePolicyCommittedView(t *testing.T) {
	for _, resume := range []bool{false, true} {
		rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
			client, ok := agent.ModelClient("registered-model")
			require.True(t, ok)
			_, err := client.Complete(ctx, &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "second"}}}}})
			return err
		})
		view := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "first"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "second"}}}}
		input.View = testSeedView(t, rt, view[:1], "root-run")
		data, err := callrecord.Encode(view[1])
		require.NoError(t, err)
		hash, err := rt.RunStore.Put(t.Context(), "root-run", data)
		require.NoError(t, err)
		entries, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
			return rt.RunStore.Get(ctx, "root-run", hash)
		}, input.View.IndexHead)
		require.NoError(t, err)
		segment, err := index.NewSegment(input.View.IndexHead, entries[0].Chain, []index.MessageEntry{{ID: 2, Kind: index.Message, Hash: hash}})
		require.NoError(t, err)
		input.View.Pending = segment.Entries
		reg := rt.agents[input.AgentID]
		reg.Policy.History = func(_ context.Context, messages []*model.Message, _ []*model.ToolDefinition) ([]*model.Message, error) {
			messages[0].Meta = map[string]any{"policy_mutated": true}
			return messages[1:], nil
		}
		rt.agents[input.AgentID] = reg
		ctx := engine.WithActivityAttempt(t.Context(), 1)
		var out *PlanActivityOutput
		if resume {
			out, err = rt.PlanResumeActivity(ctx, input)
		} else {
			out, err = rt.PlanStartActivity(ctx, input)
		}
		require.NoError(t, err)
		calls, err := rt.RunStore.ListCalls(ctx, "root-run", "child-run")
		require.NoError(t, err)
		require.Len(t, calls, 1)
		started, err := callrecord.DecodeStarted(calls[0].Started)
		require.NoError(t, err)
		require.NotNil(t, started.Request.View)
		assert.Equal(t, out.IndexHead, started.Request.View.IndexHead)
		assert.Empty(t, started.Request.View.Pending)
		assert.Equal(t, []callrecord.MessageSegment{{ViewFrom: 1, ViewTo: 2}}, started.Request.Messages)
		_, err = callrecord.Rebuild(ctx, rt.RunStore, "root-run", started, func(ctx context.Context, recorded index.ViewRef) ([]*model.Message, error) {
			loaded, err := rt.loadTestView(ctx, "root-run", "child-run", recorded)
			if err != nil {
				return nil, err
			}
			return loaded.Messages, nil
		})
		require.NoError(t, err)
	}
}
