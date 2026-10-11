package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingFactsClient struct {
	*recordingTestClient
	limits       model.ContextLimits
	resolved     string
	lookups      int
	requestModel string
	mutate       bool
}

func TestCallRecordingFactsTerminalPaths(t *testing.T) {
	failure := errors.New("provider failure")
	overflow := fmt.Errorf("wrapped: %w", model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "overflow", "", false, nil))
	usage := model.TokenUsage{InputTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 30, TotalTokens: 60}
	rejection, err := model.RestoreOutputValidationError(model.OutputValidationOutputBounds, failure, model.ResponseEvidence{}, &usage)
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		stream  bool
		failure error
		input   int
		outcome contextmeasure.RequestOutcome
	}{
		{"unary", false, nil, 60, contextmeasure.OutcomeOK},
		{"unary_overflow", false, overflow, 60, contextmeasure.OutcomeOverflow},
		{"unary_error", false, failure, 60, contextmeasure.OutcomeError},
		{"unary_validation", false, rejection, 60, contextmeasure.OutcomeError},
		{"no_usage", false, nil, 0, contextmeasure.OutcomeOK},
		{"setup_error", true, failure, 0, contextmeasure.OutcomeError},
		{"setup_overflow", true, overflow, 0, contextmeasure.OutcomeOverflow},
		{"finalize", true, nil, 60, contextmeasure.OutcomeOK},
		{"finalize_overflow", true, overflow, 60, contextmeasure.OutcomeOverflow},
		{"close", true, nil, 60, contextmeasure.OutcomeOK},
		{"close_early", true, nil, 0, contextmeasure.OutcomeError},
		{"abandoned_usage", true, nil, 60, contextmeasure.OutcomeError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New()}
			response := &model.Response{Usage: usage}
			inner := &recordingTestStream{chunks: []model.Chunk{model.UsageChunk{Usage: usage}}, response: response, terminal: io.EOF}
			provider := &recordingTestClient{store: store, response: response, stream: inner}
			recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
			client := &callRecordingClient{inner: provider, recording: recording}
			ctx := recordingCallContext(1)
			req := &model.Request{Model: "provider", MaxTokens: 50, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "abc"}}}}}
			if !tc.stream {
				provider.err = tc.failure
				if tc.name == "unary_validation" {
					provider.response = nil
				} else if tc.name == "no_usage" {
					provider.response.Usage = model.TokenUsage{}
				}
				_, err := client.Complete(ctx, req)
				require.ErrorIs(t, err, tc.failure)
			} else {
				if tc.name == "setup_error" || tc.name == "setup_overflow" {
					provider.err = tc.failure
				}
				stream, err := client.Stream(ctx, req)
				if provider.err != nil {
					require.ErrorIs(t, err, tc.failure)
				} else {
					require.NoError(t, err)
					started, err := store.ListCalls(ctx, "root", "run")
					require.NoError(t, err)
					require.Len(t, started, 1)
					assert.Equal(t, runstore.CallStarted, started[0].State)
					assert.Empty(t, started[0].Completion)
					assert.NotContains(t, string(started[0].Started), `"facts"`)
					if tc.name != "close_early" {
						_, err = stream.Recv()
						require.NoError(t, err)
					}
					if tc.name != "abandoned_usage" && tc.name != "close_early" {
						_, err = stream.Recv()
						require.ErrorIs(t, err, io.EOF)
					}
					switch tc.name {
					case "close", "close_early":
						err = stream.Close()
						if tc.name == "close_early" {
							require.Error(t, err)
						} else {
							require.NoError(t, err)
						}
					case "abandoned_usage":
						require.NoError(t, recording.Finish(ctx))
					default:
						require.ErrorIs(t, stream.Finalize(tc.failure), tc.failure)
					}
				}
			}
			require.NoError(t, recording.Finish(ctx))
			calls, err := store.ListCalls(ctx, "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			assert.Equal(t, &contextmeasure.RequestFacts{Call: calls[0].Key, Model: "provider", OutputReserve: 50, TextBytes: 3, InputTokens: tc.input, Outcome: tc.outcome}, completion.Facts)
			state := runstore.CallFailed
			if tc.outcome == contextmeasure.OutcomeOK {
				state = runstore.CallCompleted
			}
			assert.Equal(t, state, calls[0].State)
		})
	}
}

func TestCallRecordingFactsRegisteredLimits(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		overrides              map[string]model.ContextLimits
		explicit               int
		window, reserve, image int
		rewritten              bool
	}{
		{"adapter", nil, 0, 1000, 120, 700, false},
		{"resolved_override", map[string]model.ContextLimits{"canonical": {ContextWindow: 9000, DefaultMaxTokens: 90, ImageTokens: 333}, "registered": {ContextWindow: 5000}}, 0, 9000, 90, 333, false},
		{"registered_override", map[string]model.ContextLimits{"registered": {ContextWindow: 7000, DefaultMaxTokens: 80, ImageTokens: 444}}, 0, 7000, 80, 444, false},
		{"explicit_reserve", nil, 60, 1000, 60, 700, false},
		{"unknown_window", nil, 0, 0, 120, 700, false},
		{"rewritten_view", nil, 0, 1000, 120, 700, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithContextLimits(tc.overrides))
			store := &recordingTestStore{Store: rt.RunStore}
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: store, response: &model.Response{}}, limits: model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 200, DefaultMaxTokens: 120, ImageTokens: 700}, resolved: "canonical", mutate: true}
			if tc.window == 0 {
				provider.limits.ContextWindow = 0
			}
			require.NoError(t, rt.RegisterModel("registered", provider))
			registered, ok := rt.ModelClient("registered")
			require.True(t, ok)
			view := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "abc"}}, Meta: map[string]any{"id": "original"}}}
			recorder := NewCallRecording(store, "root", index.ViewRef{}, view, 0)
			view[0].Parts[0] = model.TextPart{Text: "mutated caller view"}
			req := &model.Request{Model: "alias", MaxTokens: tc.explicit, Messages: []*model.Message{
				{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "abc"}, model.CacheCheckpointPart{}}, Meta: map[string]any{"id": "changed"}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}},
			}}
			if tc.rewritten {
				req.Messages[1].Parts[0] = model.TextPart{Text: "xyz"}
			}
			client := &callRecordingClient{inner: registered, recording: recorder}
			_, err := client.Complete(recordingCallContext(1), req)
			require.NoError(t, err)
			calls, err := store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			assert.Equal(t, &contextmeasure.RequestFacts{Call: calls[0].Key, Model: "canonical", ContextWindow: tc.window, OutputReserve: tc.reserve, TextBytes: 9, FixedBytes: 6, NonText: tc.image, ViewBearing: !tc.rewritten, Outcome: contextmeasure.OutcomeOK}, completion.Facts)
			assert.Equal(t, 1, provider.lookups)
			assert.Equal(t, "alias", provider.requestModel)
			assert.Equal(t, "mutated provider request", req.Model)
		})
	}
}

func TestCallRecordingFactsEffectiveInterceptorRequest(t *testing.T) {
	rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Model: "original", MaxTokens: 10})
		return err
	})
	provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}}, resolved: "canonical", limits: model.ContextLimits{ContextWindow: 1000}}
	require.NoError(t, rt.RegisterModel("registered-model", provider))
	rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
		req := *input.Request
		req.Model, req.MaxTokens = "rewritten", 30
		return &BeforeModelDecision{Request: &req}, nil
	}}}
	_, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	calls, err := rt.RunStore.ListCalls(t.Context(), "root-run", "child-run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	completion, err := callrecord.DecodeCompletion(calls[0].Completion)
	require.NoError(t, err)
	wantRequest := &model.Request{Model: "canonical", MaxTokens: 30}
	ensureToolUnavailableDefinition(wantRequest)
	want, err := contextmeasure.Measure(wantRequest, provider.limits, nil)
	require.NoError(t, err)
	want.Call, want.Outcome = calls[0].Key, contextmeasure.OutcomeOK
	assert.Equal(t, &want, completion.Facts)
	assert.Equal(t, "rewritten", provider.requestModel)
}

func (c *recordingFactsClient) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	c.lookups++
	c.requestModel = req.Model
	return c.limits, c.resolved, c.limits.ContextWindow > 0
}

func (c *recordingFactsClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if c.mutate {
		req.Model, req.MaxTokens = "mutated provider request", 99999
		req.Messages[0].Parts[0] = model.TextPart{Text: "mutated"}
	}
	return c.recordingTestClient.Complete(ctx, req)
}
