// Package planner defines helpers for streaming model responses into planner
// results and events. This file provides StreamSummary and the canonical stream
// consumption helpers for planners that use streaming model clients.
package planner

import (
	"context"
	"errors"
	"io"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// StreamSummary aggregates the outcome of a streaming LLM invocation. Planners
// can use the collected text/tool calls when constructing their PlanResult.
type StreamSummary struct {
	// Text accumulates assistant text chunks in the order they were received.
	Text string
	// ToolCalls captures tool invocations requested by the model (if any).
	ToolCalls []ToolRequest
	// Usage reports the canonical terminal response usage.
	Usage model.TokenUsage
	// StopReason records the provider stop reason when emitted.
	StopReason string
}

// StreamObserver receives every chunk accepted by the validated stream. Returning
// an error stops consumption; that exact error is passed to Finalize and remains
// available through StreamConsumptionError.PrimaryError.
type StreamObserver interface {
	OnChunk(context.Context, model.Chunk) error
}

// StreamObserverFunc adapts a function to StreamObserver.
type StreamObserverFunc func(context.Context, model.Chunk) error

// StreamConsumptionError separates the error that stopped consumption from the
// result of finalizing the validated stream. This lets callers preserve a
// consumer error while independently classifying or sanitizing provider cleanup
// details. Both errors remain discoverable with errors.Is and errors.As.
type StreamConsumptionError struct {
	primary      error
	finalization error
}

// ConsumeStream drains the provided streamer and returns the aggregated
// StreamSummary so planners can produce a final response or schedule tool calls.
// It emits planner events for text and thinking chunks unless the streamer
// reports that it owns planner event emission. Runtime planner events publish a
// provisional presentation live and stage only its canonical commitment until
// successful finalization. Callers are responsible for handling ToolCalls in
// the resulting summary.
func ConsumeStream(ctx context.Context, streamer model.ValidatedStreamer, ev PlannerEvents) (StreamSummary, error) {
	summary, _, err := ConsumeStreamWithObserver(ctx, streamer, ev, nil)
	return summary, err
}

// ConsumeStreamWithObserver drains a validated stream while invoking observer
// for every accepted chunk. The helper owns literal-EOF detection, canonical
// response retrieval, and exactly-once finalization. An observer error stops
// consumption and is supplied unchanged to Finalize. The canonical response is
// returned only after the caller-facing stream has produced literal io.EOF.
// A nil observer disables the additional callback.
func ConsumeStreamWithObserver(ctx context.Context, streamer model.ValidatedStreamer, ev PlannerEvents, observer StreamObserver) (StreamSummary, *model.Response, error) {
	var summary StreamSummary
	if err := validateStreamInputs(streamer, ev); err != nil {
		return summary, nil, err
	}
	emitEvents := true
	if owned, ok := streamer.(interface{ EmitsPlannerEvents() bool }); ok {
		emitEvents = !owned.EmitsPlannerEvents()
	}
	presentation := newStreamPresentation(ctx, ev, emitEvents)

	for {
		chunk, err := streamer.Recv()
		if err != nil {
			//nolint:errorlint // Only literal EOF proves validated completion.
			if err == io.EOF {
				break
			}
			return summary, nil, presentation.finalize(streamer, nil, err)
		}
		handleStreamChunk(ctx, ev, &summary, chunk, emitEvents, presentation)
		if observer != nil {
			if err := observer.OnChunk(ctx, chunk); err != nil {
				return summary, nil, presentation.finalize(streamer, nil, err)
			}
		}
	}

	response := streamer.Response()
	if response == nil {
		return summary, nil, presentation.finalize(streamer, nil, errors.New("validated model stream ended without a canonical response"))
	}
	summary.Usage = response.Usage
	summary.StopReason = response.StopReason

	return summary, response, presentation.finalize(streamer, response, nil)
}

// OnChunk invokes f.
func (f StreamObserverFunc) OnChunk(ctx context.Context, chunk model.Chunk) error {
	return f(ctx, chunk)
}

// Error returns the error that stopped consumption. It returns the finalization
// error only when consumption reached clean EOF.
func (e *StreamConsumptionError) Error() string {
	if e.primary != nil {
		return e.primary.Error()
	}
	return e.finalization.Error()
}

// Unwrap exposes both the primary and finalization errors.
func (e *StreamConsumptionError) Unwrap() []error {
	if e.primary == nil {
		return []error{e.finalization}
	}
	if e.finalization == nil {
		return []error{e.primary}
	}
	return []error{e.primary, e.finalization}
}

// PrimaryError returns the exact receive, observer, or terminal-protocol error
// supplied to Finalize. It is nil when only finalization failed after clean EOF.
func (e *StreamConsumptionError) PrimaryError() error {
	return e.primary
}

// FinalizationError returns the result from ValidatedStreamer.Finalize.
func (e *StreamConsumptionError) FinalizationError() error {
	return e.finalization
}

type streamPresentation struct {
	ctx    context.Context
	events ModelPresentationEvents
	id     string
}

func newStreamPresentation(ctx context.Context, events PlannerEvents, emitEvents bool) *streamPresentation {
	presentation := &streamPresentation{ctx: ctx}
	if !emitEvents {
		return presentation
	}
	presentation.events, _ = events.(ModelPresentationEvents)
	if presentation.events != nil {
		presentation.id = presentation.events.StartModelPresentation(ctx)
	}
	return presentation
}

func (p *streamPresentation) stageText(text string) {
	if p.events == nil || text == "" {
		return
	}
	p.events.PublishModelText(p.ctx, p.id, text)
}

func (p *streamPresentation) stageThinking(block model.ThinkingPart) {
	if p.events == nil {
		return
	}
	p.events.PublishModelThinking(p.ctx, p.id, block)
}

func (p *streamPresentation) finalize(streamer model.ValidatedStreamer, response *model.Response, primaryErr error) error {
	finalizationErr := streamer.Finalize(primaryErr)
	consumptionErr := newStreamConsumptionError(primaryErr, finalizationErr)
	if p.events == nil {
		return consumptionErr
	}
	if consumptionErr != nil {
		p.events.FinishModelPresentation(p.ctx, p.id, false)
		return consumptionErr
	}
	commitErr := p.events.CommitModelPresentation(p.ctx, p.id, response)
	if commitErr != nil {
		p.events.FinishModelPresentation(p.ctx, p.id, false)
		return commitErr
	}
	p.events.FinishModelPresentation(p.ctx, p.id, true)
	return nil
}

func newStreamConsumptionError(primaryErr, finalizationErr error) error {
	if primaryErr == nil && finalizationErr == nil {
		return nil
	}
	return &StreamConsumptionError{primary: primaryErr, finalization: finalizationErr}
}
func validateStreamInputs(streamer model.ValidatedStreamer, ev PlannerEvents) error {
	if streamer == nil {
		return errors.New("nil streamer")
	}
	if ev == nil {
		return errors.New("nil PlannerEvents")
	}
	return nil
}

func handleStreamChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.Chunk, emitEvents bool, presentation *streamPresentation) {
	switch value := chunk.(type) {
	case model.TextChunk:
		handleTextChunk(ctx, ev, summary, value, emitEvents, presentation)
	case model.ThinkingChunk:
		if emitEvents {
			handleThinkingChunk(ctx, ev, value, presentation)
		}
	case model.ToolCallChunk:
		handleToolCallChunk(summary, value)
	case model.ToolCallDeltaChunk:
		if emitEvents && presentation.events == nil {
			handleToolCallDeltaChunk(ctx, ev, value)
		}
	case model.UsageChunk:
		handleUsageChunk(ctx, ev, summary, value, emitEvents)
	case model.StopChunk:
		summary.StopReason = value.Reason
	}
}

func handleTextChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.TextChunk, emitEvents bool, presentation *streamPresentation) {
	delta := textChunkDelta(chunk)
	if delta == "" {
		return
	}
	summary.Text += delta
	if emitEvents {
		if presentation.events != nil {
			presentation.stageText(delta)
		} else {
			ev.AssistantChunk(ctx, delta)
		}
	}
}

func textChunkDelta(chunk model.TextChunk) string {
	if len(chunk.Message.Parts) == 0 {
		return ""
	}
	var delta string
	for _, p := range chunk.Message.Parts {
		switch part := p.(type) {
		case model.TextPart:
			delta += part.Text
		case model.CitationsPart:
			delta += part.Text
		}
	}
	return delta
}

func handleThinkingChunk(ctx context.Context, ev PlannerEvents, chunk model.ThinkingChunk, presentation *streamPresentation) {
	for _, p := range chunk.Message.Parts {
		if tp, ok := p.(model.ThinkingPart); ok {
			if presentation.events != nil {
				presentation.stageThinking(tp)
			} else {
				ev.PlannerThinkingBlock(ctx, tp)
			}
		}
	}
}

func handleToolCallChunk(summary *StreamSummary, chunk model.ToolCallChunk) {
	if chunk.ToolCall.Name == "" {
		return
	}
	summary.ToolCalls = append(summary.ToolCalls, ToolRequest{
		Name:       chunk.ToolCall.Name,
		Payload:    chunk.ToolCall.Payload,
		ToolCallID: chunk.ToolCall.ID,
	})
}

func handleToolCallDeltaChunk(ctx context.Context, ev PlannerEvents, chunk model.ToolCallDeltaChunk) {
	if chunk.Delta.ID == "" || chunk.Delta.Delta == "" {
		return
	}
	ev.ToolCallArgsDelta(ctx, chunk.Delta.ID, chunk.Delta.Name, chunk.Delta.Delta)
}

func handleUsageChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.UsageChunk, emitEvents bool) {
	summary.Usage = addUsage(summary.Usage, chunk.Usage)
	if emitEvents {
		ev.UsageDelta(ctx, chunk.Usage)
	}
}

func addUsage(current, delta model.TokenUsage) model.TokenUsage {
	return model.TokenUsage{
		InputTokens:      current.InputTokens + delta.InputTokens,
		OutputTokens:     current.OutputTokens + delta.OutputTokens,
		TotalTokens:      current.TotalTokens + delta.TotalTokens,
		CacheReadTokens:  current.CacheReadTokens + delta.CacheReadTokens,
		CacheWriteTokens: current.CacheWriteTokens + delta.CacheWriteTokens,
	}
}
