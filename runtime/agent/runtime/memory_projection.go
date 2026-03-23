package runtime

import (
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

func projectMemoryEvent(event hooks.Event) (agentID, runID string, memEvent memory.Event, ok bool) {
	switch evt := event.(type) {
	case *hooks.ToolCallScheduledEvent:
		return evt.AgentID(), evt.RunID(), memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ToolCallData{
			ToolCallID:            evt.ToolCallID,
			ParentToolCallID:      evt.ParentToolCallID,
			ToolName:              evt.ToolName,
			PayloadJSON:           evt.Payload,
			Queue:                 evt.Queue,
			ExpectedChildrenTotal: evt.ExpectedChildrenTotal,
		}, nil), true
	case *hooks.ToolResultReceivedEvent:
		return evt.AgentID(), evt.RunID(), memory.NewEvent(time.UnixMilli(evt.Timestamp()), newToolResultMemoryData(evt), nil), true
	case *hooks.AssistantMessageEvent:
		return evt.AgentID(), evt.RunID(), memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.AssistantMessageData{
			Message:    evt.Message,
			Structured: evt.Structured,
		}, nil), true
	case *hooks.ThinkingBlockEvent:
		return evt.AgentID(), evt.RunID(), memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ThinkingData{
			Text:         evt.Text,
			Signature:    evt.Signature,
			Redacted:     evt.Redacted,
			ContentIndex: evt.ContentIndex,
			Final:        evt.Final,
		}, nil), true
	case *hooks.PlannerNoteEvent:
		return evt.AgentID(), evt.RunID(), memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.PlannerNoteData{
			Note: evt.Note,
		}, evt.Labels), true
	default:
		return "", "", memory.Event{}, false
	}
}

func newToolResultMemoryData(evt *hooks.ToolResultReceivedEvent) memory.ToolResultData {
	errorMessage := ""
	if evt.Error != nil {
		errorMessage = evt.Error.Error()
	}
	return memory.ToolResultData{
		ToolCallID:       evt.ToolCallID,
		ParentToolCallID: evt.ParentToolCallID,
		ToolName:         evt.ToolName,
		ResultJSON:       evt.ResultJSON,
		ServerData:       evt.ServerData,
		Preview:          evt.ResultPreview,
		Bounds:           evt.Bounds,
		Duration:         evt.Duration,
		Telemetry:        evt.Telemetry,
		RetryHint:        toMemoryRetryHint(evt.RetryHint),
		ErrorMessage:     errorMessage,
	}
}

func toMemoryRetryHint(hint *planner.RetryHint) *memory.RetryHintData {
	if hint == nil {
		return nil
	}
	return &memory.RetryHintData{
		Reason:             string(hint.Reason),
		Tool:               hint.Tool,
		RestrictToTool:     hint.RestrictToTool,
		MissingFields:      append([]string(nil), hint.MissingFields...),
		ExampleInput:       cloneAnyMap(hint.ExampleInput),
		PriorInput:         cloneAnyMap(hint.PriorInput),
		ClarifyingQuestion: hint.ClarifyingQuestion,
		Message:            hint.Message,
	}
}

func cloneAnyMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
