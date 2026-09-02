package runtime

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

func projectMemoryEvents(event hooks.Event) (agentID, runID string, memEvents []memory.Event, ok bool) {
	switch evt := event.(type) {
	case *hooks.ToolCallScheduledEvent:
		return evt.AgentID(), evt.RunID(), []memory.Event{memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ToolCallData{
			ToolCallID:            evt.ToolCallID,
			ParentToolCallID:      evt.ParentToolCallID,
			ToolName:              evt.ToolName,
			PayloadJSON:           evt.Payload,
			Queue:                 evt.Queue,
			ExpectedChildrenTotal: evt.ExpectedChildrenTotal,
		}, nil)}, true
	case *hooks.ToolResultReceivedEvent:
		return evt.AgentID(), evt.RunID(), []memory.Event{memory.NewEvent(time.UnixMilli(evt.Timestamp()), newToolResultMemoryData(evt), nil)}, true
	case *hooks.AssistantMessageEvent:
		return evt.AgentID(), evt.RunID(), []memory.Event{memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.AssistantMessageData{
			Message:    evt.Message,
			Structured: evt.Structured,
		}, nil)}, true
	case *hooks.AssistantTurnCommittedEvent:
		if !evt.ContentEventsOmitted {
			return "", "", nil, false
		}
		return projectCommittedAssistantMemoryEvents(evt)
	case *hooks.ThinkingBlockEvent:
		return evt.AgentID(), evt.RunID(), []memory.Event{memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ThinkingData{
			Text:         evt.Text,
			Signature:    evt.Signature,
			Redacted:     evt.Redacted,
			ContentIndex: evt.ContentIndex,
			Final:        evt.Final,
		}, nil)}, true
	case *hooks.PlannerNoteEvent:
		return evt.AgentID(), evt.RunID(), []memory.Event{memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.PlannerNoteData{
			Note: evt.Note,
		}, evt.Labels)}, true
	default:
		return "", "", nil, false
	}
}

func projectCommittedAssistantMemoryEvents(evt *hooks.AssistantTurnCommittedEvent) (agentID, runID string, memEvents []memory.Event, ok bool) {
	messages := evt.Messages
	if len(messages) == 0 && evt.Message != nil {
		messages = []*model.Message{evt.Message}
	}
	timestamp := time.UnixMilli(evt.Timestamp())
	var events []memory.Event
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			switch value := part.(type) {
			case model.ThinkingPart:
				events = append(events, memory.NewEvent(timestamp, memory.ThinkingData{
					Text:         value.Text,
					Signature:    value.Signature,
					Redacted:     value.Redacted,
					ContentIndex: value.Index,
					Final:        value.Final,
				}, nil))
			case model.TextPart:
				events = append(events, memory.NewEvent(timestamp, memory.AssistantMessageData{Message: value.Text}, nil))
			case model.CitationsPart:
				events = append(events, memory.NewEvent(timestamp, memory.AssistantMessageData{Message: value.Text}, nil))
			}
		}
	}
	if len(events) == 0 {
		return "", "", nil, false
	}
	return evt.AgentID(), evt.RunID(), events, true
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
		Artifacts:        cloneArtifactRefs(evt.Artifacts),
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
