package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
)

// RunCompletedEvent.Status values emitted by the workflow runtime.
const (
	completionStatusSuccess  = "success"
	completionStatusFailed   = "failed"
	completionStatusCanceled = "canceled"
)

type (
	// Subscriber receives runtime events and forwards certain ones to a
	// stream.Sink, such as a WebSocket, SSE, or message bus. It acts as a
	// bridge between the internal event bus and an external stream client.
	//
	// Only the sink actually "sends" messages; the subscriber listens for
	// incoming events, translates those of interest, and hands them off to
	// the sink using its Send method.
	//
	// The following hook events are streamed to clients:
	//   - AssistantMessage      → EventAssistantReply
	//   - PlannerNote           → EventPlannerThought
	//   - PromptRendered        → EventPromptRendered
	//   - ToolCallArgsDelta     → EventToolCallArgsDelta (optional)
	//   - ToolCallScheduled     → EventToolStart
	//   - ToolCallUpdated       → EventToolUpdate
	//   - ToolResultReceived    → EventToolEnd
	//
	// All other (internal) events, such as workflow lifecycle changes, are
	// ignored and not sent to clients.
	Subscriber struct {
		sink    Sink
		profile StreamProfile
	}
)

// NewSubscriber constructs a subscriber that forwards selected hook
// events to the provided stream sink using the default stream profile.
// The sink is typically backed by a message bus like Pulse or a direct
// WebSocket/SSE connection.
//
// NewSubscriber returns an error if sink is nil, as the subscriber
// requires a valid sink to function.
//
// Example:
//
//	sink := myStreamImplementation
//	sub, err := hooks.NewSubscriber(sink)
//	if err != nil {
//	    return err
//	}
//	subscription, _ := bus.Register(sub)
//	defer subscription.Close()
func NewSubscriber(sink Sink) (*Subscriber, error) {
	return NewSubscriberWithProfile(sink, DefaultProfile())
}

// NewSubscriberWithProfile constructs a subscriber that forwards selected
// hook events to the provided stream sink, applying the given StreamProfile
// to determine which event kinds are emitted.
func NewSubscriberWithProfile(sink Sink, profile StreamProfile) (*Subscriber, error) {
	if sink == nil {
		return nil, errors.New("stream sink is required")
	}
	return &Subscriber{
		sink:    sink,
		profile: profile,
	}, nil
}

// HandleEvent implements the Subscriber interface by translating hook events
// into stream events and forwarding them to the configured sink.
//
// Event translation:
//   - AssistantMessage → EventAssistantReply
//   - PlannerNote → EventPlannerThought
//   - PromptRendered → EventPromptRendered
//   - ToolCallArgsDelta → EventToolCallArgsDelta (optional)
//   - ToolCallScheduled → EventToolStart
//   - ToolCallUpdated → EventToolUpdate
//   - ToolResultReceived → EventToolEnd
//   - All other event types are ignored (return nil)
//
// If the sink returns an error, HandleEvent propagates it to the bus, which
// stops event delivery to remaining subscribers. This fail-fast behavior
// ensures that streaming failures are visible to the runtime.
func (s *Subscriber) HandleEvent(ctx context.Context, event hooks.Event) error {
	if err, handled := s.handleAwaitEvent(ctx, event); handled {
		return err
	}
	if err, handled := s.handleToolEvent(ctx, event); handled {
		return err
	}
	if err, handled := s.handleMessageEvent(ctx, event); handled {
		return err
	}
	if err, handled := s.handleWorkflowEvent(ctx, event); handled {
		return err
	}
	if err, handled := s.handleUsageEvent(ctx, event); handled {
		return err
	}
	return nil
}

func (s *Subscriber) handleUsageEvent(ctx context.Context, event hooks.Event) (error, bool) {
	switch evt := event.(type) {
	case *hooks.UsageEvent:
		if !s.profile.Usage {
			return nil, true
		}
		payload := UsagePayload{TokenUsage: evt.TokenUsage}
		return s.sink.Send(ctx, Usage{
			Base: newBaseFromHook(evt, EventUsage, payload),
			Data: payload,
		}), true
	default:
		return nil, false
	}
}

func (s *Subscriber) handleAwaitEvent(ctx context.Context, event hooks.Event) (error, bool) {
	switch evt := event.(type) {
	case *hooks.AwaitClarificationEvent:
		if !s.profile.AwaitClarification {
			return nil, true
		}
		payload := AwaitClarificationPayload{
			ID:             evt.ID,
			Question:       evt.Question,
			MissingFields:  append([]string(nil), evt.MissingFields...),
			RestrictToTool: string(evt.RestrictToTool),
			ExampleInput:   evt.ExampleInput,
		}
		return s.sink.Send(ctx, AwaitClarification{
			Base: newBaseFromHook(evt, EventAwaitClarification, payload),
			Data: payload,
		}), true
	case *hooks.AwaitConfirmationEvent:
		if !s.profile.AwaitConfirmation {
			return nil, true
		}
		payload := AwaitConfirmationPayload{
			ID:         evt.ID,
			Title:      evt.Title,
			Prompt:     evt.Prompt,
			ToolName:   string(evt.ToolName),
			ToolCallID: evt.ToolCallID,
			Payload:    evt.Payload,
		}
		return s.sink.Send(ctx, AwaitConfirmation{
			Base: newBaseFromHook(evt, EventAwaitConfirmation, payload),
			Data: payload,
		}), true
	case *hooks.AwaitQuestionsEvent:
		if !s.profile.AwaitQuestions {
			return nil, true
		}
		qs := make([]AwaitQuestionPayload, 0, len(evt.Questions))
		for _, q := range evt.Questions {
			opts := make([]AwaitQuestionOptionPayload, 0, len(q.Options))
			for _, o := range q.Options {
				opts = append(opts, AwaitQuestionOptionPayload{
					ID:    o.ID,
					Label: o.Label,
				})
			}
			qs = append(qs, AwaitQuestionPayload{
				ID:            q.ID,
				Prompt:        q.Prompt,
				AllowMultiple: q.AllowMultiple,
				Options:       opts,
			})
		}
		payload := AwaitQuestionsPayload{
			ID:         evt.ID,
			ToolName:   string(evt.ToolName),
			ToolCallID: evt.ToolCallID,
			Title:      evt.Title,
			Questions:  qs,
		}
		return s.sink.Send(ctx, AwaitQuestions{
			Base: newBaseFromHook(evt, EventAwaitQuestions, payload),
			Data: payload,
		}), true
	case *hooks.AwaitExternalToolsEvent:
		if !s.profile.AwaitExternalTools {
			return nil, true
		}
		items := make([]AwaitToolPayload, 0, len(evt.Items))
		for _, it := range evt.Items {
			items = append(items, AwaitToolPayload{
				ToolName:   string(it.ToolName),
				ToolCallID: it.ToolCallID,
				Payload:    it.Payload,
			})
		}
		payload := AwaitExternalToolsPayload{ID: evt.ID, Items: items}
		return s.sink.Send(ctx, AwaitExternalTools{
			Base: newBaseFromHook(evt, EventAwaitExternalTools, payload),
			Data: payload,
		}), true
	case *hooks.ToolAuthorizationEvent:
		if !s.profile.ToolAuthorization {
			return nil, true
		}
		payload := ToolAuthorizationPayload{
			ToolName:   string(evt.ToolName),
			ToolCallID: evt.ToolCallID,
			Approved:   evt.Approved,
			Summary:    evt.Summary,
			ApprovedBy: evt.ApprovedBy,
		}
		return s.sink.Send(ctx, ToolAuthorization{
			Base: newBaseFromHook(evt, EventToolAuthorization, payload),
			Data: payload,
		}), true
	default:
		return nil, false
	}
}

func (s *Subscriber) handleToolEvent(ctx context.Context, event hooks.Event) (error, bool) {
	switch evt := event.(type) {
	case *hooks.ToolCallArgsDeltaEvent:
		if !s.profile.ToolCallArgsDelta {
			return nil, true
		}
		if evt.ToolCallID == "" || evt.Delta == "" {
			return nil, true
		}
		if evt.ToolName == "" {
			return fmt.Errorf("tool_call_args_delta missing tool name for tool_call_id %q", evt.ToolCallID), true
		}
		payload := ToolCallArgsDeltaPayload{
			ToolCallID: evt.ToolCallID,
			ToolName:   string(evt.ToolName),
			Delta:      evt.Delta,
		}
		return s.sink.Send(ctx, ToolCallArgsDelta{
			Base: newBaseFromHook(evt, EventToolCallArgsDelta, payload),
			Data: payload,
		}), true
	case *hooks.ToolCallScheduledEvent:
		if !s.profile.ToolStart {
			return nil, true
		}
		payload := ToolStartPayload{
			ToolCallID:            evt.ToolCallID,
			ToolName:              string(evt.ToolName),
			Payload:               evt.Payload,
			Queue:                 evt.Queue,
			ParentToolCallID:      evt.ParentToolCallID,
			ExpectedChildrenTotal: evt.ExpectedChildrenTotal,
			DisplayHint:           evt.DisplayHint,
		}
		return s.sink.Send(ctx, ToolStart{
			Base: newBaseFromHook(evt, EventToolStart, payload),
			Data: payload,
		}), true
	case *hooks.ToolResultReceivedEvent:
		if !s.profile.ToolEnd {
			return nil, true
		}
		if evt.ToolCallID == "" {
			return errors.New("stream: tool_end missing tool_call_id"), true
		}
		if evt.ToolName == "" {
			return errors.New("stream: tool_end missing tool_name"), true
		}
		payload := ToolEndPayload{
			ToolCallID:       evt.ToolCallID,
			ParentToolCallID: evt.ParentToolCallID,
			ToolName:         string(evt.ToolName),
			Result:           evt.ResultJSON,
			Bounds:           evt.Bounds,
			Duration:         evt.Duration,
			Telemetry:        evt.Telemetry,
			RetryHint:        evt.RetryHint,
			Error:            evt.Error,
		}
		if preview := clampPreview(evt.ResultPreview); preview != "" {
			payload.ResultPreview = preview
		}
		return s.sink.Send(ctx, ToolEnd{
			Base:       newBaseFromHook(evt, EventToolEnd, payload),
			ServerData: append(rawjson.Message(nil), evt.ServerData...),
			Data:       payload,
		}), true
	case *hooks.ToolCallUpdatedEvent:
		if !s.profile.ToolUpdate {
			return nil, true
		}
		up := ToolUpdatePayload{
			ToolCallID:            evt.ToolCallID,
			ExpectedChildrenTotal: evt.ExpectedChildrenTotal,
		}
		return s.sink.Send(ctx, ToolUpdate{
			Base: newBaseFromHook(evt, EventToolUpdate, up),
			Data: up,
		}), true
	case *hooks.ChildRunLinkedEvent:
		if !s.profile.ChildRuns {
			return nil, true
		}
		payload := ChildRunLinkedPayload{
			ToolName:     string(evt.ToolName),
			ToolCallID:   evt.ToolCallID,
			ChildRunID:   evt.ChildRunID,
			ChildAgentID: evt.ChildAgentID,
		}
		return s.sink.Send(ctx, ChildRunLinked{
			Base: newBaseFromHook(evt, EventChildRunLinked, payload),
			Data: payload,
		}), true
	default:
		return nil, false
	}
}

func (s *Subscriber) handleMessageEvent(ctx context.Context, event hooks.Event) (error, bool) {
	switch evt := event.(type) {
	case *hooks.AssistantMessageEvent:
		if !s.profile.Assistant {
			return nil, true
		}
		payload := AssistantReplyPayload{Text: evt.Message}
		return s.sink.Send(ctx, AssistantReply{
			Base: newBaseFromHook(evt, EventAssistantReply, payload),
			Data: payload,
		}), true
	case *hooks.PlannerNoteEvent:
		if !s.profile.Thoughts {
			return nil, true
		}
		payload := PlannerThoughtPayload{Note: evt.Note}
		return s.sink.Send(ctx, PlannerThought{
			Base: newBaseFromHook(evt, EventPlannerThought, payload),
			Data: payload,
		}), true
	case *hooks.PromptRenderedEvent:
		if !s.profile.PromptRendered {
			return nil, true
		}
		payload := PromptRenderedPayload{
			PromptID: evt.PromptID.String(),
			Version:  evt.Version,
			Scope:    evt.Scope,
		}
		return s.sink.Send(ctx, PromptRendered{
			Base: newBaseFromHook(evt, EventPromptRendered, payload),
			Data: payload,
		}), true
	case *hooks.ThinkingBlockEvent:
		if !s.profile.Thoughts {
			return nil, true
		}
		payload := PlannerThoughtPayload{
			Text:         evt.Text,
			Signature:    evt.Signature,
			Redacted:     evt.Redacted,
			ContentIndex: evt.ContentIndex,
			Final:        evt.Final,
		}
		if !evt.Final && evt.Text != "" {
			payload.Note = evt.Text
		}
		return s.sink.Send(ctx, PlannerThought{
			Base: newBaseFromHook(evt, EventPlannerThought, payload),
			Data: payload,
		}), true
	default:
		return nil, false
	}
}

func (s *Subscriber) handleWorkflowEvent(ctx context.Context, event hooks.Event) (error, bool) {
	switch evt := event.(type) {
	case *hooks.RunCompletedEvent:
		if !s.profile.Workflow {
			return nil, true
		}
		return s.emitRunCompleted(ctx, evt), true
	case *hooks.RunPhaseChangedEvent:
		if !s.profile.Workflow {
			return nil, true
		}
		if evt.Phase == run.PhaseCompleted || evt.Phase == run.PhaseFailed || evt.Phase == run.PhaseCanceled {
			return nil, true
		}
		payload := WorkflowPayload{Phase: string(evt.Phase)}
		return s.sink.Send(ctx, Workflow{
			Base: newBaseFromHook(evt, EventWorkflow, payload),
			Data: payload,
		}), true
	default:
		return nil, false
	}
}

func (s *Subscriber) emitRunCompleted(ctx context.Context, evt *hooks.RunCompletedEvent) error {
	phase := string(evt.Phase)
	if phase == "" {
		return fmt.Errorf("run_completed event missing phase for run %s", evt.RunID())
	}
	payload := WorkflowPayload{
		Phase:          phase,
		Status:         evt.Status,
		ErrorProvider:  evt.ErrorProvider,
		ErrorOperation: evt.ErrorOperation,
		ErrorKind:      evt.ErrorKind,
		ErrorCode:      evt.ErrorCode,
		HTTPStatus:     evt.HTTPStatus,
		Retryable:      evt.Retryable,
	}
	if evt.Error != nil {
		payload.DebugError = evt.Error.Error()
	}
	if evt.Status == completionStatusFailed {
		payload.Error = evt.PublicError
	}
	if err := s.sink.Send(ctx, Workflow{
		Base: newBaseFromHook(evt, EventWorkflow, payload),
		Data: payload,
	}); err != nil {
		return err
	}
	return s.sink.Send(ctx, RunStreamEnd{
		Base: newBaseFromHook(evt, EventRunStreamEnd, RunStreamEndPayload{}),
		Data: RunStreamEndPayload{},
	})
}

func newBaseFromHook(evt hooks.Event, eventType EventType, payload any) Base {
	return NewBaseWithEventKey(eventType, evt.RunID(), evt.SessionID(), payload, evt.EventKey())
}

// clampPreview normalizes whitespace and clamps result previews to a reasonable
// length for UI display.
func clampPreview(in string) string {
	if in == "" {
		return ""
	}
	// normalize whitespace
	out := make([]rune, 0, len(in))
	prevSpace := false
	for _, r := range in {
		switch r {
		case '\n', '\r', '\t', ' ':
			if !prevSpace {
				out = append(out, ' ')
			}
			prevSpace = true
		default:
			out = append(out, r)
			prevSpace = false
		}
	}
	const max = 140
	if len(out) <= max {
		return string(out)
	}
	return string(out[:max])
}
