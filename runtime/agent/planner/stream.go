// Package planner defines helpers for streaming model responses into planner
// results and events. This file provides StreamSummary and ConsumeStream for
// planners that work with streaming model clients.
package planner

import (
	"context"
	"errors"
	"io"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

// StreamSummary aggregates the outcome of a streaming LLM invocation. Planners
// can use the collected text/tool calls when constructing their PlanResult.
type StreamSummary struct {
	// Text accumulates assistant text chunks in the order they were received.
	Text string
	// ToolCalls captures tool invocations requested by the model (if any).
	ToolCalls []ToolRequest
	// Usage aggregates the reported token usage across usage chunks/metadata.
	Usage model.TokenUsage
	// StopReason records the provider stop reason when emitted.
	StopReason string
}

// ConsumeStream drains the provided streamer, emitting planner events for text and
// thinking chunks via the provided PlannerEvents. It returns the aggregated
// StreamSummary so planners can produce a final response or schedule tool calls.
// Callers are responsible for handling ToolCalls in the resulting summary.
func ConsumeStream(ctx context.Context, streamer model.Streamer, ev PlannerEvents) (StreamSummary, error) {
	var summary StreamSummary
	if err := validateStreamInputs(streamer, ev); err != nil {
		return summary, err
	}
	defer func() {
		_ = streamer.Close()
	}()

	for {
		chunk, err := streamer.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return summary, err
		}
		handleStreamChunk(ctx, ev, &summary, chunk)
	}

	applyStreamMetadataUsage(ctx, ev, &summary, streamer.Metadata())

	return summary, nil
}

func validateStreamInputs(streamer model.Streamer, ev PlannerEvents) error {
	if streamer == nil {
		return errors.New("nil streamer")
	}
	if ev == nil {
		return errors.New("nil PlannerEvents")
	}
	return nil
}

func handleStreamChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.Chunk) {
	switch chunk.Type {
	case model.ChunkTypeText:
		handleTextChunk(ctx, ev, summary, chunk)
	case model.ChunkTypeThinking:
		handleThinkingChunk(ctx, ev, chunk)
	case model.ChunkTypeToolCall:
		handleToolCallChunk(summary, chunk)
	case model.ChunkTypeToolCallDelta:
		handleToolCallDeltaChunk(ctx, ev, chunk)
	case model.ChunkTypeUsage:
		handleUsageChunk(ctx, ev, summary, chunk)
	case model.ChunkTypeStop:
		summary.StopReason = chunk.StopReason
	}
}

func handleTextChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.Chunk) {
	delta := textChunkDelta(chunk)
	if delta == "" {
		return
	}
	summary.Text += delta
	ev.AssistantChunk(ctx, delta)
}

func textChunkDelta(chunk model.Chunk) string {
	if chunk.Message == nil || len(chunk.Message.Parts) == 0 {
		return ""
	}
	var delta string
	for _, p := range chunk.Message.Parts {
		if tp, ok := p.(model.TextPart); ok && tp.Text != "" {
			delta += tp.Text
		}
	}
	return delta
}

func handleThinkingChunk(ctx context.Context, ev PlannerEvents, chunk model.Chunk) {
	if chunk.Message == nil {
		return
	}
	for _, p := range chunk.Message.Parts {
		if tp, ok := p.(model.ThinkingPart); ok {
			ev.PlannerThinkingBlock(ctx, tp)
		}
	}
}

func handleToolCallChunk(summary *StreamSummary, chunk model.Chunk) {
	if chunk.ToolCall.Name == "" {
		return
	}
	summary.ToolCalls = append(summary.ToolCalls, ToolRequest{
		Name:       chunk.ToolCall.Name,
		Payload:    chunk.ToolCall.Payload,
		ToolCallID: chunk.ToolCall.ID,
	})
}

func handleToolCallDeltaChunk(ctx context.Context, ev PlannerEvents, chunk model.Chunk) {
	if chunk.ToolCallDelta == nil || chunk.ToolCallDelta.ID == "" || chunk.ToolCallDelta.Delta == "" {
		return
	}
	ev.ToolCallArgsDelta(ctx, chunk.ToolCallDelta.ID, chunk.ToolCallDelta.Name, chunk.ToolCallDelta.Delta)
}

func handleUsageChunk(ctx context.Context, ev PlannerEvents, summary *StreamSummary, chunk model.Chunk) {
	if chunk.UsageDelta == nil {
		return
	}
	summary.Usage = addUsage(summary.Usage, *chunk.UsageDelta)
	ev.UsageDelta(ctx, *chunk.UsageDelta)
}

func applyStreamMetadataUsage(ctx context.Context, ev PlannerEvents, summary *StreamSummary, meta map[string]any) {
	if meta == nil {
		return
	}
	usage, ok := meta["usage"].(model.TokenUsage)
	if !ok {
		return
	}
	summary.Usage = addUsage(summary.Usage, usage)
	ev.UsageDelta(ctx, usage)
}

func addUsage(current, delta model.TokenUsage) model.TokenUsage {
	return model.TokenUsage{
		InputTokens:  current.InputTokens + delta.InputTokens,
		OutputTokens: current.OutputTokens + delta.OutputTokens,
		TotalTokens:  current.TotalTokens + delta.TotalTokens,
	}
}
