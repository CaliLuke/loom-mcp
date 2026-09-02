// Package planner defines helpers for streaming model responses into planner
// results and events. This file provides StreamSummary and ConsumeStream for
// planners that work with streaming model clients.
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

// ConsumeStream drains the provided streamer and returns the aggregated
// StreamSummary so planners can produce a final response or schedule tool calls.
// It emits planner events for text and thinking chunks unless the streamer
// reports that it owns planner event emission. A runtime-owned stream stages
// those events until successful finalization. Callers are responsible for
// handling ToolCalls in the resulting summary.
func ConsumeStream(ctx context.Context, streamer model.ValidatedStreamer, ev PlannerEvents) (StreamSummary, error) {
	var summary StreamSummary
	if err := validateStreamInputs(streamer, ev); err != nil {
		return summary, err
	}
	emitEvents := true
	if owned, ok := streamer.(interface{ EmitsPlannerEvents() bool }); ok {
		emitEvents = !owned.EmitsPlannerEvents()
	}

	for {
		chunk, err := streamer.Recv()
		if err != nil {
			//nolint:errorlint // Only literal EOF proves validated completion.
			if err == io.EOF {
				break
			}
			return summary, finalizeStream(streamer, err)
		}
		handleStreamChunk(ctx, ev, &summary, chunk, emitEvents)
	}

	response := streamer.Response()
	if response == nil {
		return summary, finalizeStream(streamer, errors.New("validated model stream ended without a canonical response"))
	}
	summary.Usage = response.Usage
	summary.StopReason = response.StopReason

	return summary, finalizeStream(streamer, nil)
}

func finalizeStream(streamer model.ValidatedStreamer, primaryErr error) error {
	return streamer.Finalize(primaryErr)
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

func handleStreamChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.Chunk, emitEvents bool) {
	switch value := chunk.(type) {
	case model.TextChunk:
		handleTextChunk(ctx, ev, summary, value, emitEvents)
	case model.ThinkingChunk:
		if emitEvents {
			handleThinkingChunk(ctx, ev, value)
		}
	case model.ToolCallChunk:
		handleToolCallChunk(summary, value)
	case model.ToolCallDeltaChunk:
		if emitEvents {
			handleToolCallDeltaChunk(ctx, ev, value)
		}
	case model.UsageChunk:
		handleUsageChunk(ctx, ev, summary, value, emitEvents)
	case model.StopChunk:
		summary.StopReason = value.Reason
	}
}

func handleTextChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.TextChunk, emitEvents bool) {
	delta := textChunkDelta(chunk)
	if delta == "" {
		return
	}
	summary.Text += delta
	if emitEvents {
		ev.AssistantChunk(ctx, delta)
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

func handleThinkingChunk(ctx context.Context, ev PlannerEvents, chunk model.ThinkingChunk) {
	for _, p := range chunk.Message.Parts {
		if tp, ok := p.(model.ThinkingPart); ok {
			ev.PlannerThinkingBlock(ctx, tp)
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
