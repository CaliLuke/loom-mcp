package runtime

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type consumeStreamPlanner struct{}

type wrapperRawProvider struct {
	response *model.Response
	stream   model.Streamer
}

type directToolUnavailablePlanner struct{}

type streamedResponseToolUnavailablePlanner struct{}

func (p *wrapperRawProvider) Complete(context.Context, *model.Request) (*model.Response, error) {
	return p.response, nil
}

func (p *wrapperRawProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return p.stream, nil
}

func (directToolUnavailablePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	response, err := client.Complete(ctx, &model.Request{
		Model: "requested",
		Tools: []*model.ToolDefinition{{
			Name:        "svc.read",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{ToolCalls: make([]planner.ToolRequest, len(response.ToolCalls))}
	for index, call := range response.ToolCalls {
		result.ToolCalls[index] = planner.ToolRequest{Name: call.Name, Payload: call.Payload, ToolCallID: call.ID}
	}
	return result, nil
}

func (directToolUnavailablePlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func (streamedResponseToolUnavailablePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	stream, err := client.Stream(ctx, &model.Request{
		Model: "requested",
		Tools: []*model.ToolDefinition{{
			Name:        "svc.read",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		return nil, err
	}
	for {
		_, err = stream.Recv()
		//nolint:errorlint // Only literal EOF proves validated completion.
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, stream.Finalize(err)
		}
	}
	response := stream.Response()
	if err := stream.Finalize(nil); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("validated stream returned no response")
	}
	result := &planner.PlanResult{ToolCalls: make([]planner.ToolRequest, len(response.ToolCalls))}
	for index, call := range response.ToolCalls {
		result.ToolCalls[index] = planner.ToolRequest{Name: call.Name, Payload: call.Payload, ToolCallID: call.ID}
	}
	return result, nil
}

func (streamedResponseToolUnavailablePlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func (consumeStreamPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return consumePlannerStream(ctx, input.Agent, input.Events)
}

func (consumeStreamPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return consumePlannerStream(ctx, input.Agent, input.Events)
}

func consumePlannerStream(ctx context.Context, agentContext planner.PlannerContext, events planner.PlannerEvents) (*planner.PlanResult, error) {
	client, ok := agentContext.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	stream, err := client.Stream(ctx, &model.Request{Model: "requested"})
	if err != nil {
		return nil, err
	}
	summary, err := planner.ConsumeStream(ctx, stream, events)
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: summary.Text}},
	}}}, nil
}

type wrapperEvents struct {
	text       []string
	thinking   []model.ThinkingPart
	toolDeltas []string
	usage      []model.TokenUsage
}

func (e *wrapperEvents) AssistantChunk(_ context.Context, text string) {
	e.text = append(e.text, text)
}

func (e *wrapperEvents) ToolCallArgsDelta(_ context.Context, _ string, _ tools.Ident, delta string) {
	e.toolDeltas = append(e.toolDeltas, delta)
}

func (e *wrapperEvents) PlannerThinkingBlock(_ context.Context, thinking model.ThinkingPart) {
	e.thinking = append(e.thinking, thinking)
}

func (*wrapperEvents) PlannerThought(context.Context, string, map[string]string) {
}

func (e *wrapperEvents) UsageDelta(_ context.Context, usage model.TokenUsage) {
	e.usage = append(e.usage, usage)
}

type wrapperLifecycleStream struct {
	chunks        []model.Chunk
	index         int
	response      *model.Response
	terminalErr   error
	closeErr      error
	finalizeInput error
	closeCalls    int
	eof           bool
	builder       model.StreamResponseBuilder
}

func (s *wrapperLifecycleStream) Recv() (model.Chunk, error) {
	if s.index == len(s.chunks) {
		if s.terminalErr != nil {
			err := s.terminalErr
			s.terminalErr = nil
			return nil, err
		}
		s.eof = true
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	if err := s.builder.Add(chunk); err != nil {
		return nil, err
	}
	return chunk, nil
}

func (s *wrapperLifecycleStream) Close() error {
	s.closeCalls++
	return s.closeErr
}

func (s *wrapperLifecycleStream) Response() *model.Response {
	if !s.eof {
		return nil
	}
	if s.response == nil {
		return s.builder.Response()
	}
	return s.response
}

func (s *wrapperLifecycleStream) Finalize(primaryErr error) error {
	s.finalizeInput = primaryErr
	return errors.Join(primaryErr, s.Close())
}

func TestEventStreamAndConsumeStreamEmitEachChunkOnce(t *testing.T) {
	usage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
	response := &model.Response{Usage: usage, StopReason: "end_turn"}
	inner := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: "hello"},
				model.CitationsPart{Text: " cited", Citations: []model.Citation{{Title: "source"}}},
			}}},
			model.ThinkingChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "consider"}}}},
			model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "lookup", Delta: `{"q":`}},
			model.UsageChunk{Usage: usage},
		},
		response: response,
	}
	events := &wrapperEvents{}
	client := newEventDecoratedClient(contractModelClient{streamer: inner}, events)
	stream, err := client.Stream(context.Background(), &model.Request{Model: "requested"})
	require.NoError(t, err)
	summary, err := planner.ConsumeStream(context.Background(), stream, events)
	require.NoError(t, err)

	assert.Equal(t, "hello cited", summary.Text)
	assert.Equal(t, response.Usage, summary.Usage)
	assert.Equal(t, []string{"hello", " cited"}, events.text)
	assert.Equal(t, []model.ThinkingPart{{Text: "consider"}}, events.thinking)
	assert.Equal(t, []string{`{"q":`}, events.toolDeltas)
	require.Len(t, events.usage, 1)
	assert.Equal(t, "requested", events.usage[0].Model)
	assert.Same(t, response, stream.Response())

	require.NoError(t, inner.finalizeInput)
	assert.Equal(t, 1, inner.closeCalls)
}

func TestPlannerContextModelClientAndConsumeStreamDoNotDuplicateTranscriptOrUsage(t *testing.T) {
	t.Parallel()

	usage := model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
	inner := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "hello"}}}},
			model.ThinkingChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "consider", Final: true}}}},
			model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "lookup", Delta: `{"q":`}},
			model.UsageChunk{Usage: usage},
			model.StopChunk{Reason: "end_turn"},
		},
		response: &model.Response{Usage: usage, StopReason: "end_turn"},
	}
	rt := New()
	rt.models["default"] = contractModelClient{streamer: inner}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-consume-stream",
		RunContext: run.Context{RunID: "run-consume-stream", Attempt: 1},
	})
	require.NoError(t, err)
	expectedUsage := usage
	expectedUsage.Model = ""
	require.Equal(t, expectedUsage, out.Usage)
	require.Len(t, out.Transcript, 1)
	require.Equal(t, "hello", agentMessageText(out.Transcript[0]))
	require.Len(t, out.Transcript[0].Parts, 2)
	var textParts, thinkingParts int
	for _, part := range out.Transcript[0].Parts {
		switch part.(type) {
		case model.TextPart:
			textParts++
		case model.ThinkingPart:
			thinkingParts++
		}
	}
	require.Equal(t, 1, textParts)
	require.Equal(t, 1, thinkingParts)
	require.Equal(t, 1, inner.closeCalls)
}

func TestEventStreamDoesNotEmitInternalToolArguments(t *testing.T) {
	t.Parallel()

	inner := &wrapperLifecycleStream{chunks: []model.Chunk{model.ToolCallDeltaChunk{
		Delta: model.ToolCallDelta{
			ID:    "internal-call",
			Name:  tools.ToolUnavailable,
			Delta: `{"available_tools":["private.secret"]}`,
		},
	}}}
	events := &wrapperEvents{}
	client := newEventDecoratedClient(contractModelClient{streamer: inner}, events)
	stream, err := client.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)
	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, tools.ToolUnavailable, chunk.(model.ToolCallDeltaChunk).Delta.Name)
	require.Empty(t, events.toolDeltas)
}

func TestEventStreamCanonicalizesDirectInternalToolFromExactRequest(t *testing.T) {
	t.Parallel()

	inner := &wrapperLifecycleStream{
		chunks: []model.Chunk{model.ToolCallChunk{
			ToolCall: model.ToolCall{
				ID:      "internal-call",
				Name:    tools.ToolUnavailable,
				Payload: []byte(`{"available_tools":["svc.write"]}`),
			},
		}},
		response: &model.Response{StopReason: "tool_use"},
	}
	events := &wrapperEvents{}
	client := newEventDecoratedClient(contractModelClient{streamer: inner}, events)
	stream, err := client.Stream(context.Background(), &model.Request{Tools: []*model.ToolDefinition{
		{Name: "svc.read"},
		{Name: tools.ToolUnavailable.String()},
	}})
	require.NoError(t, err)
	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(chunk.(model.ToolCallChunk).ToolCall.Payload))
}

func TestRejectedStreamDoesNotPersistPresentationContent(t *testing.T) {
	t.Parallel()

	const secret = "rejected-stream-secret"
	rawStream := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{
			Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: secret},
			}},
		},
		model.StopChunk{Reason: "max_tokens", OutputLimited: true},
	}}
	client, err := model.NewClient(&wrapperRawProvider{stream: rawStream})
	require.NoError(t, err)
	runlog := &recordingRunlog{}
	rt := New(WithRunEventStore(runlog))
	rt.models["default"] = client
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-rejected-stream",
		RunContext: run.Context{RunID: "run-rejected-stream", Attempt: 1},
	})
	require.NoError(t, err)
	require.NotNil(t, out.Recovery)
	require.Equal(t, model.OutputValidationOutputBounds, out.Recovery.Kind)
	require.Empty(t, out.Transcript)
	for _, event := range runlog.events {
		require.NotContains(t, string(event.Payload), secret)
	}
}

func TestFailedStreamFinalizationDoesNotPersistStagedContent(t *testing.T) {
	t.Parallel()

	const secret = "uncommitted-stream-secret"
	tests := []struct {
		name   string
		stream *wrapperLifecycleStream
	}{
		{
			name: "provider close failure",
			stream: &wrapperLifecycleStream{
				chunks: []model.Chunk{model.TextChunk{
					Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
						model.TextPart{Text: secret},
					}},
				}},
				response: &model.Response{StopReason: "end_turn"},
				closeErr: errors.New("provider close failed"),
			},
		},
		{
			name: "wrapped eof",
			stream: &wrapperLifecycleStream{
				chunks: []model.Chunk{model.TextChunk{
					Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
						model.TextPart{Text: secret},
					}},
				}},
				response:    &model.Response{StopReason: "end_turn"},
				terminalErr: errors.Join(io.EOF, errors.New("wrapped terminal failure")),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runlog := &recordingRunlog{}
			rt := New(WithRunEventStore(runlog))
			events := newPlannerEvents(rt, "svc.agent", "run-failed-finalize", "", "")
			client := newEventDecoratedClient(contractModelClient{streamer: test.stream}, events)
			stream, err := client.Stream(context.Background(), &model.Request{Model: "requested"})
			require.NoError(t, err)
			_, err = planner.ConsumeStream(context.Background(), stream, events)
			require.Error(t, err)
			require.Empty(t, events.exportTranscript())
			for _, event := range runlog.events {
				require.NotContains(t, string(event.Payload), secret)
			}
		})
	}
}

func TestDirectInternalToolUsesNarrowModelCatalogUnderBroadPolicy(t *testing.T) {
	t.Parallel()

	provider := &wrapperRawProvider{response: &model.Response{
		ToolCalls: []model.ToolCall{{
			ID:      "direct-internal",
			Name:    tools.ToolUnavailable,
			Payload: []byte(`{"available_tools":["svc.write"]}`),
		}},
		StopReason: "tool_use",
	}}
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	rt := New()
	rt.toolSpecs["svc.read"] = tools.ToolSpec{Name: "svc.read"}
	rt.toolSpecs["svc.write"] = tools.ToolSpec{Name: "svc.write"}
	rt.models["default"] = client
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: directToolUnavailablePlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-narrow-catalog",
		RunContext:       run.Context{RunID: "run-narrow-catalog", Attempt: 1},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.read", "svc.write", tools.ToolUnavailable},
	})
	require.NoError(t, err)
	require.Len(t, out.Result.ToolCalls, 1)
	require.Equal(t, tools.ToolUnavailable, out.Result.ToolCalls[0].Name)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(out.Result.ToolCalls[0].Payload))
}

func TestStreamResponseInternalToolUsesNarrowModelCatalogUnderBroadPolicy(t *testing.T) {
	t.Parallel()

	rawStream := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.ToolCallChunk{
			ToolCall: model.ToolCall{
				ID:      "direct-internal",
				Name:    tools.ToolUnavailable,
				Payload: []byte(`{"available_tools":["svc.write"]}`),
			},
		},
		model.StopChunk{Reason: "tool_use"},
	}}
	client, err := model.NewClient(&wrapperRawProvider{stream: rawStream})
	require.NoError(t, err)
	rt := New()
	rt.toolSpecs["svc.read"] = tools.ToolSpec{Name: "svc.read"}
	rt.toolSpecs["svc.write"] = tools.ToolSpec{Name: "svc.write"}
	rt.models["default"] = client
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: streamedResponseToolUnavailablePlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-stream-narrow-catalog",
		RunContext:       run.Context{RunID: "run-stream-narrow-catalog", Attempt: 1},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.read", "svc.write", tools.ToolUnavailable},
	})
	require.NoError(t, err)
	require.Len(t, out.Result.ToolCalls, 1)
	require.Equal(t, tools.ToolUnavailable, out.Result.ToolCalls[0].Name)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(out.Result.ToolCalls[0].Payload))
}
