package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
)

type failAfterConsumedStreamPlanner struct {
	err error
}

func (p failAfterConsumedStreamPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	_, err := consumePlannerStream(ctx, input.Agent, input.Events)
	if err != nil {
		return nil, err
	}
	return nil, p.err
}

func (failAfterConsumedStreamPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

type omitFinalizePlanner struct{}

type mutatingPresentationSink struct{}

func (mutatingPresentationSink) Send(_ context.Context, event stream.Event) error {
	turn, ok := event.(stream.AssistantTurn)
	if !ok || len(turn.Data.Messages) == 0 || len(turn.Data.Messages[0].Parts) == 0 {
		return nil
	}
	turn.Data.Messages[0].Parts[0] = model.TextPart{Text: "mutated by sink"}
	turn.Data.Messages[0].Meta["provider_message_id"] = "mutated"
	return nil
}

func (mutatingPresentationSink) Close(context.Context) error {
	return nil
}

func (omitFinalizePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	client, ok := input.Agent.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	validated, err := client.Stream(ctx, &model.Request{})
	if err != nil {
		return nil, err
	}
	for {
		_, err = validated.Recv()
		//nolint:errorlint // Only literal EOF proves validated completion.
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return &planner.PlanResult{}, nil
}

func (omitFinalizePlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected resume")
}

func TestModelPresentationStreamsLiveThenAcceptsCanonicalContent(t *testing.T) {
	t.Parallel()

	rawStream := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: "hello"},
			}}},
			model.ThinkingChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.ThinkingPart{Text: "consider", Final: true},
			}}},
			model.StopChunk{Reason: "end_turn"},
		},
	}
	client, err := model.NewClient(&wrapperRawProvider{stream: rawStream})
	require.NoError(t, err)
	sink := &recordingStreamSink{}
	runlog := &recordingRunlog{}
	memoryStore := &seamMemoryStore{}
	rt := New(WithStream(sink), WithRunEventStore(runlog), WithMemoryStore(memoryStore))
	_, err = rt.CreateSession(context.Background(), "session-presentation")
	require.NoError(t, err)
	rt.models["default"] = client
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), &PlanActivityInput{
		AgentID: "svc.agent",
		RunID:   "run-presentation",
		RunContext: run.Context{
			RunID:     "run-presentation",
			SessionID: "session-presentation",
			Attempt:   1,
		},
	})
	require.NoError(t, err)
	require.Nil(t, out.Recovery)
	require.True(t, out.Result.Streamed)
	require.Len(t, out.Transcript, 2)

	events := sink.snapshot()
	require.Len(t, events, 5)
	started := events[0].(stream.ModelPresentation)
	text := events[1].(stream.AssistantReply)
	thought := events[2].(stream.PlannerThought)
	require.Equal(t, stream.EventAssistantTurn, events[3].Type())
	accepted := events[4].(stream.ModelPresentation)
	require.NotEmpty(t, started.Data.PresentationID)
	require.Equal(t, stream.ModelPresentationStarted, started.Data.State)
	require.Equal(t, started.Data.PresentationID, text.Data.PresentationID)
	require.Equal(t, "hello", text.Data.Text)
	require.Equal(t, started.Data.PresentationID, thought.Data.PresentationID)
	require.Equal(t, "consider", thought.Data.Text)
	require.Equal(t, started.Data.PresentationID, accepted.Data.PresentationID)
	require.Equal(t, stream.ModelPresentationAccepted, accepted.Data.State)
	for _, event := range events {
		require.NotEqual(t, stream.EventToolCallArgsDelta, event.Type())
	}
	for _, event := range runlog.events {
		require.NotContains(t, string(event.Payload), `{"q":`)
	}
	require.Len(t, runlog.events, 1)
	require.Equal(t, hooks.AssistantTurnCommitted, runlog.events[0].Type)
	var committed struct {
		Messages []*model.Message
	}
	require.NoError(t, json.Unmarshal(runlog.events[0].Payload, &committed))
	require.Len(t, committed.Messages, 2)
	require.Equal(t, "hello", committed.Messages[0].Parts[0].(model.TextPart).Text)
	require.Equal(t, "consider", committed.Messages[1].Parts[0].(model.ThinkingPart).Text)
	require.Len(t, memoryStore.appends, 2)
	assistant, err := memory.DecodeAssistantMessageData(memoryStore.appends[0])
	require.NoError(t, err)
	require.Equal(t, "hello", assistant.Message)
	thinking, err := memory.DecodeThinkingData(memoryStore.appends[1])
	require.NoError(t, err)
	require.Equal(t, "consider", thinking.Text)
}

func TestModelPresentationCommitsAuthoritativeResponseMessages(t *testing.T) {
	t.Parallel()

	response := &model.Response{
		Content: []model.Message{
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{model.CitationsPart{
					Text: "first",
					Citations: []model.Citation{{
						Title:         "source",
						Source:        "document-1",
						SourceContent: []string{"quoted"},
					}},
				}},
				Meta: map[string]any{"provider_message_id": "message-1"},
			},
			{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: " second"}},
				Meta:  map[string]any{"provider_message_id": "message-2"},
			},
		},
		StopReason: "end_turn",
	}
	rawStream := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: "first second"},
			}}},
			model.StopChunk{Reason: "end_turn"},
		},
		response: response,
	}
	runlogStore := &recordingRunlog{}
	rt := New(WithRunEventStore(runlogStore))
	rt.models["default"] = contractModelClient{streamer: rawStream}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), presentationPlanInput("run-authoritative", ""))

	require.NoError(t, err)
	require.Len(t, out.Transcript, 2)
	require.Equal(t, "message-1", out.Transcript[0].Meta["provider_message_id"])
	require.Equal(t, "message-2", out.Transcript[1].Meta["provider_message_id"])
	cited := out.Transcript[0].Parts[0].(model.CitationsPart)
	require.Equal(t, "first", cited.Text)
	require.Equal(t, "source", cited.Citations[0].Title)
	require.Len(t, runlogStore.events, 1)
	var committed struct {
		Messages []*model.Message
	}
	require.NoError(t, json.Unmarshal(runlogStore.events[0].Payload, &committed))
	require.Equal(t, out.Transcript, committed.Messages)
}

func TestModelPresentationCommitsResponseAboveOrdinaryHookLimit(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("x", maxHookPayloadBytes+1)
	runlogStore := &recordingRunlog{}
	rt := New(WithRunEventStore(runlogStore))
	rt.models["default"] = newValidatedPresentationClient(t, text)
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), presentationPlanInput("run-large-presentation", ""))

	require.NoError(t, err)
	require.True(t, out.Result.Streamed)
	require.Len(t, out.Transcript, 1)
	require.Equal(t, text, out.Transcript[0].Parts[0].(model.TextPart).Text)
	require.Len(t, runlogStore.events, 1)
	require.Greater(t, len(runlogStore.events[0].Payload), maxHookPayloadBytes)
}

func TestModelPresentationsCommitAtomically(t *testing.T) {
	t.Parallel()

	sink := &recordingStreamSink{}
	runlogStore := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlogStore))
	_, err := rt.CreateSession(context.Background(), "session-atomic")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-atomic", "session-atomic", "turn-1")
	presentationIDs := make([]string, 0, 2)
	for _, text := range []string{"first", "second"} {
		presentationID := events.StartModelPresentation(context.Background())
		presentationIDs = append(presentationIDs, presentationID)
		require.NoError(t, events.CommitModelPresentation(context.Background(), presentationID, &model.Response{
			Content: []model.Message{{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: text}},
			}},
		}))
		events.FinishModelPresentation(context.Background(), presentationID, true)
	}

	require.NoError(t, events.commitModelPresentations(context.Background()))

	require.Len(t, runlogStore.events, 1)
	var committed hooks.AssistantTurnCommittedEvent
	require.NoError(t, json.Unmarshal(runlogStore.events[0].Payload, &committed))
	require.Equal(t, presentationIDs, committed.PresentationIDs)
	require.Len(t, committed.Messages, 2)
	require.Equal(t, "first", committed.Messages[0].Parts[0].(model.TextPart).Text)
	require.Equal(t, "second", committed.Messages[1].Parts[0].(model.TextPart).Text)
	streamEvents := sink.snapshot()
	var assistantTurns, accepted int
	for _, event := range streamEvents {
		switch value := event.(type) {
		case stream.AssistantTurn:
			assistantTurns++
			require.Equal(t, presentationIDs, value.Data.PresentationIDs)
		case stream.ModelPresentation:
			if value.Data.State == stream.ModelPresentationAccepted {
				accepted++
			}
		}
	}
	require.Equal(t, 1, assistantTurns)
	require.Equal(t, 2, accepted)
}

func TestModelPresentationsDiscardTogetherWhenAtomicCommitFails(t *testing.T) {
	t.Parallel()

	hookErr := errors.New("atomic append failed")
	sink := &recordingStreamSink{}
	rt := New(WithStream(sink), WithRunEventStore(failingRunlogStore{err: hookErr}))
	_, err := rt.CreateSession(context.Background(), "session-atomic-failure")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-atomic-failure", "session-atomic-failure", "turn-1")
	for _, text := range []string{"first", "second"} {
		presentationID := events.StartModelPresentation(context.Background())
		require.NoError(t, events.CommitModelPresentation(context.Background(), presentationID, &model.Response{
			Content: []model.Message{{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: text}},
			}},
		}))
		events.FinishModelPresentation(context.Background(), presentationID, true)
	}

	err = events.commitModelPresentations(context.Background())

	require.ErrorIs(t, err, hookErr)
	require.Empty(t, events.exportTranscript())
	var accepted, discarded int
	for _, event := range sink.snapshot() {
		presentation, ok := event.(stream.ModelPresentation)
		if !ok {
			continue
		}
		if presentation.Data.State == stream.ModelPresentationAccepted {
			accepted++
		}
		if presentation.Data.State == stream.ModelPresentationDiscarded {
			discarded++
		}
	}
	require.Zero(t, accepted)
	require.Equal(t, 2, discarded)
}

func TestCanonicalPresentationProjectionsCannotMutateTranscript(t *testing.T) {
	t.Parallel()

	memoryStore := &seamMemoryStore{}
	runlogStore := &recordingRunlog{}
	rt := New(
		WithStream(mutatingPresentationSink{}),
		WithRunEventStore(runlogStore),
		WithMemoryStore(memoryStore),
	)
	_, err := rt.CreateSession(context.Background(), "session-owned")
	require.NoError(t, err)
	rawStream := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: "canonical"},
			}}},
			model.StopChunk{Reason: "end_turn"},
		},
		response: &model.Response{Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "canonical"}},
			Meta:  map[string]any{"provider_message_id": "message-1"},
		}}, StopReason: "end_turn"},
	}
	rt.models["default"] = contractModelClient{streamer: rawStream}
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), presentationPlanInput("run-owned", "session-owned"))

	require.NoError(t, err)
	require.Len(t, out.Transcript, 1)
	require.Equal(t, "canonical", out.Transcript[0].Parts[0].(model.TextPart).Text)
	require.Equal(t, "message-1", out.Transcript[0].Meta["provider_message_id"])
	require.Len(t, memoryStore.appends, 1)
	projected, err := memory.DecodeAssistantMessageData(memoryStore.appends[0])
	require.NoError(t, err)
	require.Equal(t, "canonical", projected.Message)
	var durable hooks.AssistantTurnCommittedEvent
	require.NoError(t, json.Unmarshal(runlogStore.events[0].Payload, &durable))
	require.Equal(t, "canonical", durable.Messages[0].Parts[0].(model.TextPart).Text)
	require.Equal(t, "message-1", durable.Messages[0].Meta["provider_message_id"])
}

func TestRunInfersStreamOwnershipAndCommitsFinalAnswerOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runlogStore := runloginmem.New()
	memoryStore := &seamMemoryStore{}
	sink := &recordingStreamSink{}
	rt := New(
		WithEngine(engineinmem.New()),
		WithRunEventStore(runlogStore),
		WithMemoryStore(memoryStore),
		WithStream(sink),
	)
	require.NoError(t, rt.RegisterAgent(ctx, AgentRegistration{
		ID:      "svc.agent",
		Planner: consumeStreamPlanner{},
		Workflow: engine.WorkflowDefinition{
			Name:    "svc.agent.workflow",
			Handler: rt.ExecuteWorkflow,
		},
		PlanActivityName:    "svc.agent.plan",
		ResumeActivityName:  "svc.agent.resume",
		ExecuteToolActivity: "svc.agent.execute_tool",
	}))
	rt.models["default"] = newValidatedPresentationClient(t, "once")
	_, err := rt.CreateSession(ctx, "session-once")
	require.NoError(t, err)

	output, err := rt.MustClient(agent.Ident("svc.agent")).Run(
		ctx,
		"session-once",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}},
		WithRunID("run-once"),
	)

	require.NoError(t, err)
	require.Equal(t, "once", agentMessageText(output.Final))
	page, err := runlogStore.List(ctx, "run-once", "", 100)
	require.NoError(t, err)
	var assistantMessages, assistantTurns int
	for _, event := range page.Events {
		if event.Type == hooks.AssistantMessage {
			assistantMessages++
		}
		if event.Type == hooks.AssistantTurnCommitted {
			assistantTurns++
		}
	}
	require.Zero(t, assistantMessages)
	require.Equal(t, 1, assistantTurns)
	var streamedTurns int
	for _, event := range sink.snapshot() {
		if event.Type() == stream.EventAssistantTurn {
			streamedTurns++
		}
	}
	require.Equal(t, 1, streamedTurns)
	var projectedAnswers int
	for _, event := range memoryStore.appends {
		if event.Type != memory.EventAssistantMessage {
			continue
		}
		data, decodeErr := memory.DecodeAssistantMessageData(event)
		require.NoError(t, decodeErr)
		if data.Message == "once" {
			projectedAnswers++
		}
	}
	require.Equal(t, 1, projectedAnswers)
}

func TestModelPresentationDoesNotExposePartialToolJSON(t *testing.T) {
	t.Parallel()

	const fragment = `{"q":`
	rawStream := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "lookup", Delta: fragment}},
	}}
	sink := &recordingStreamSink{}
	runlog := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlog))
	_, err := rt.CreateSession(context.Background(), "session-tool-delta")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-tool-delta", "session-tool-delta", "turn-1")
	wrapped := newEventDecoratedClient(contractModelClient{streamer: rawStream}, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{Tools: []*model.ToolDefinition{{
		Name:        "lookup",
		InputSchema: map[string]any{"type": "object"},
	}}})
	require.NoError(t, err)
	_, err = validated.Recv()
	require.NoError(t, err)
	require.Len(t, sink.snapshot(), 1)
	require.IsType(t, stream.ModelPresentation{}, sink.snapshot()[0])
	require.Empty(t, runlog.events)
	require.Error(t, validated.Finalize(errors.New("abort")))

	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 2)
	require.Equal(t, stream.ModelPresentationDiscarded, streamEvents[1].(stream.ModelPresentation).Data.State)
}

func TestModelPresentationCommitsToolOnlyAcceptanceBoundary(t *testing.T) {
	t.Parallel()

	rawStream := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.ToolCallChunk{ToolCall: model.ToolCall{ID: "call-1", Name: "lookup", Payload: []byte(`{"q":"loom"}`)}},
		model.StopChunk{Reason: "tool_use"},
	}}
	sink := &recordingStreamSink{}
	runlog := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlog))
	_, err := rt.CreateSession(context.Background(), "session-tool-only")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-tool-only", "session-tool-only", "turn-1")
	wrapped := newEventDecoratedClient(contractModelClient{streamer: rawStream}, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{Tools: []*model.ToolDefinition{{
		Name:        "lookup",
		InputSchema: map[string]any{"type": "object"},
	}}})
	require.NoError(t, err)

	summary, err := planner.ConsumeStream(context.Background(), validated, events)

	require.NoError(t, err)
	require.NoError(t, events.commitModelPresentations(context.Background()))
	require.Len(t, summary.ToolCalls, 1)
	require.Empty(t, events.exportTranscript())
	require.Len(t, runlog.events, 1)
	require.Equal(t, hooks.AssistantTurnCommitted, runlog.events[0].Type)
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 3)
	require.Equal(t, stream.ModelPresentationStarted, streamEvents[0].(stream.ModelPresentation).Data.State)
	require.Equal(t, stream.EventAssistantTurn, streamEvents[1].Type())
	require.Equal(t, stream.ModelPresentationAccepted, streamEvents[2].(stream.ModelPresentation).Data.State)
}

func TestModelPresentationDiscardsRejectedContentExactlyOnce(t *testing.T) {
	t.Parallel()

	rawStream := &wrapperLifecycleStream{
		chunks: []model.Chunk{
			model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.TextPart{Text: "incomplete"},
			}}},
			model.StopChunk{Reason: "max_tokens", OutputLimited: true},
		},
	}
	client, err := model.NewClient(&wrapperRawProvider{stream: rawStream})
	require.NoError(t, err)
	sink := &recordingStreamSink{}
	runlog := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlog))
	_, err = rt.CreateSession(context.Background(), "session-rejected")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-rejected", "session-rejected", "turn-1")
	wrapped := newEventDecoratedClient(client, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)
	for {
		_, err = validated.Recv()
		if err != nil {
			break
		}
	}
	require.Error(t, err)
	firstFinalize := validated.Finalize(err)
	require.Error(t, firstFinalize)
	require.Equal(t, firstFinalize, validated.Finalize(errors.New("different")))
	require.Empty(t, events.exportTranscript())
	require.Empty(t, runlog.events)

	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 3)
	started := streamEvents[0].(stream.ModelPresentation)
	delta := streamEvents[1].(stream.AssistantReply)
	discarded := streamEvents[2].(stream.ModelPresentation)
	require.Equal(t, started.Data.PresentationID, delta.Data.PresentationID)
	require.Equal(t, started.Data.PresentationID, discarded.Data.PresentationID)
	require.Equal(t, stream.ModelPresentationDiscarded, discarded.Data.State)
}

func TestConsumeStreamUsesPresentationForUndecoratedRuntimeStream(t *testing.T) {
	t.Parallel()

	rawStream := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
			model.TextPart{Text: "direct"},
		}}},
		model.ToolCallDeltaChunk{Delta: model.ToolCallDelta{ID: "call-1", Name: "lookup", Delta: `{"q":`}},
		model.StopChunk{Reason: "end_turn"},
	}}
	sink := &recordingStreamSink{}
	runlog := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlog))
	_, err := rt.CreateSession(context.Background(), "session-undecorated")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-undecorated", "session-undecorated", "turn-1")

	summary, err := planner.ConsumeStream(context.Background(), rawStream, events)

	require.NoError(t, err)
	require.NoError(t, events.commitModelPresentations(context.Background()))
	require.Equal(t, "direct", summary.Text)
	require.Len(t, events.exportTranscript(), 1)
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 4)
	require.Equal(t, stream.EventModelPresentation, streamEvents[0].Type())
	require.Equal(t, stream.EventAssistantReply, streamEvents[1].Type())
	require.Equal(t, stream.EventAssistantTurn, streamEvents[2].Type())
	require.Equal(t, stream.EventModelPresentation, streamEvents[3].Type())
	require.Equal(t, stream.ModelPresentationAccepted, streamEvents[3].(stream.ModelPresentation).Data.State)
	for _, event := range streamEvents {
		require.NotEqual(t, stream.EventToolCallArgsDelta, event.Type())
	}
}

func TestModelPresentationStreamFailureIsBestEffort(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("client disconnected")
	rt := New(WithStream(failingStreamSink{err: streamErr}), WithRunEventStore(&recordingRunlog{}))
	_, err := rt.CreateSession(context.Background(), "session-disconnected")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-disconnected", "session-disconnected", "turn-1")
	wrapped := newEventDecoratedClient(contractModelClient{streamer: &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
			model.TextPart{Text: "still canonical"},
		}}},
		model.StopChunk{Reason: "end_turn"},
	}}}, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)

	_, err = planner.ConsumeStream(context.Background(), validated, events)

	require.NoError(t, err)
	require.NoError(t, events.commitModelPresentations(context.Background()))
	require.Len(t, events.exportTranscript(), 1)
	require.Equal(t, "still canonical", events.exportTranscript()[0].Parts[0].(model.TextPart).Text)
}

func TestModelPresentationNoopsAfterSessionEnds(t *testing.T) {
	t.Parallel()

	sink := &recordingStreamSink{}
	rt := New(WithStream(sink), WithRunEventStore(&recordingRunlog{}))
	_, err := rt.CreateSession(context.Background(), "session-ended")
	require.NoError(t, err)
	_, err = rt.SessionStore.EndSession(context.Background(), "session-ended", time.Now().UTC())
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-ended", "session-ended", "turn-1")
	wrapped := newEventDecoratedClient(contractModelClient{streamer: &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
			model.TextPart{Text: "canonical only"},
		}}},
		model.StopChunk{Reason: "end_turn"},
	}}}, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)

	_, err = planner.ConsumeStream(context.Background(), validated, events)

	require.NoError(t, err)
	require.NoError(t, events.commitModelPresentations(context.Background()))
	require.Empty(t, sink.snapshot())
	require.Len(t, events.exportTranscript(), 1)
}

func TestModelPresentationDiscardsWhenCanonicalCommitFails(t *testing.T) {
	t.Parallel()

	sink := &recordingStreamSink{}
	hookErr := errors.New("canonical append failed")
	rt := New(WithStream(sink), WithRunEventStore(failingRunlogStore{err: hookErr}))
	_, err := rt.CreateSession(context.Background(), "session-hook-failure")
	require.NoError(t, err)
	events := newPlannerEvents(rt, "svc.agent", "run-hook-failure", "session-hook-failure", "turn-1")
	wrapped := newEventDecoratedClient(contractModelClient{streamer: &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
			model.TextPart{Text: "first"},
			model.TextPart{Text: "second"},
		}}},
		model.StopChunk{Reason: "end_turn"},
	}}}, events)
	validated, err := wrapped.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)

	_, err = planner.ConsumeStream(context.Background(), validated, events)
	require.NoError(t, err)

	err = events.commitModelPresentations(context.Background())
	require.ErrorIs(t, err, hookErr)
	require.ErrorIs(t, events.hookError(), hookErr)
	require.Empty(t, events.exportTranscript())
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 4)
	require.Equal(t, stream.ModelPresentationDiscarded, streamEvents[3].(stream.ModelPresentation).Data.State)
}

func TestModelPresentationDiscardsWhenPlannerFailsAfterStreamValidation(t *testing.T) {
	t.Parallel()

	plannerErr := errors.New("planner failed after model validation")
	sink := &recordingStreamSink{}
	runlogStore := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlogStore))
	_, err := rt.CreateSession(context.Background(), "session-planner-failure")
	require.NoError(t, err)
	rt.models["default"] = newValidatedPresentationClient(t, "not accepted")
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: failAfterConsumedStreamPlanner{err: plannerErr}}

	_, err = rt.PlanStartActivity(context.Background(), presentationPlanInput("run-planner-failure", "session-planner-failure"))

	require.ErrorIs(t, err, plannerErr)
	require.Empty(t, runlogStore.events)
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 3)
	require.Equal(t, stream.ModelPresentationStarted, streamEvents[0].(stream.ModelPresentation).Data.State)
	require.Equal(t, stream.EventAssistantReply, streamEvents[1].Type())
	require.Equal(t, stream.ModelPresentationDiscarded, streamEvents[2].(stream.ModelPresentation).Data.State)
}

func TestCanonicalPresentationCommitBypassesEventMutation(t *testing.T) {
	t.Parallel()

	interceptorCalled := false
	runlogStore := &recordingRunlog{}
	rt := New(
		WithRunEventStore(runlogStore),
		WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
			interceptorCalled = true
			return &BeforeEventDecision{Drop: true}, nil
		}}),
	)
	rt.models["default"] = newValidatedPresentationClient(t, "canonical")
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), presentationPlanInput("run-interceptor", ""))

	require.NoError(t, err)
	require.False(t, interceptorCalled)
	require.Len(t, runlogStore.events, 1)
	require.Equal(t, hooks.AssistantTurnCommitted, runlogStore.events[0].Type)
	require.Len(t, out.Transcript, 1)
	require.Equal(t, "canonical", out.Transcript[0].Parts[0].(model.TextPart).Text)
}

func TestCanonicalPresentationIgnoresPostCommitSubscriberFailure(t *testing.T) {
	t.Parallel()

	bus := hooks.NewBus()
	runlogStore := &recordingRunlog{}
	sink := &recordingStreamSink{}
	rt := New(WithHooks(bus), WithRunEventStore(runlogStore), WithStream(sink))
	_, err := rt.CreateSession(context.Background(), "session-subscriber-failure")
	require.NoError(t, err)
	_, err = rt.registerSubscriber(bus, hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
		if event.Type() == hooks.AssistantTurnCommitted {
			return errors.New("observer failed after append")
		}
		return nil
	}), SubscriberCritical)
	require.NoError(t, err)
	rt.models["default"] = newValidatedPresentationClient(t, "accepted")
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: consumeStreamPlanner{}}

	out, err := rt.PlanStartActivity(context.Background(), presentationPlanInput("run-subscriber-failure", "session-subscriber-failure"))

	require.NoError(t, err)
	require.Len(t, out.Transcript, 1)
	require.Len(t, runlogStore.events, 1)
	streamEvents := sink.snapshot()
	require.Equal(t, stream.EventAssistantTurn, streamEvents[len(streamEvents)-2].Type())
	require.Equal(t, stream.ModelPresentationAccepted, streamEvents[len(streamEvents)-1].(stream.ModelPresentation).Data.State)
}

func TestModelPresentationRejectsPlannerThatOmitsFinalize(t *testing.T) {
	t.Parallel()

	sink := &recordingStreamSink{}
	runlogStore := &recordingRunlog{}
	rt := New(WithStream(sink), WithRunEventStore(runlogStore))
	_, err := rt.CreateSession(context.Background(), "session-no-finalize")
	require.NoError(t, err)
	rt.models["default"] = newValidatedPresentationClient(t, "unfinished")
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: omitFinalizePlanner{}}

	_, err = rt.PlanStartActivity(context.Background(), presentationPlanInput("run-no-finalize", "session-no-finalize"))

	require.ErrorContains(t, err, "was not finalized")
	require.Empty(t, runlogStore.events)
	streamEvents := sink.snapshot()
	require.Equal(t, stream.ModelPresentationDiscarded, streamEvents[len(streamEvents)-1].(stream.ModelPresentation).Data.State)
}

func newValidatedPresentationClient(t *testing.T, text string) model.Client {
	t.Helper()
	raw := &wrapperLifecycleStream{chunks: []model.Chunk{
		model.TextChunk{Message: model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: text}}}},
		model.StopChunk{Reason: "end_turn"},
	}}
	client, err := model.NewClient(&wrapperRawProvider{stream: raw})
	require.NoError(t, err)
	return client
}

func presentationPlanInput(runID, sessionID string) *PlanActivityInput {
	return &PlanActivityInput{
		AgentID: "svc.agent",
		RunID:   runID,
		RunContext: run.Context{
			RunID:     runID,
			SessionID: sessionID,
			Attempt:   1,
		},
	}
}
