package runtime

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
)

// newRunSnapshot derives a compact run state snapshot by replaying canonical
// run log events in order. The caller must supply events ordered oldest-first.
func newRunSnapshot(events []*runlog.Event) (*run.Snapshot, error) {
	if len(events) == 0 {
		return nil, run.ErrNotFound
	}

	s := &run.Snapshot{
		RunID:     events[0].RunID,
		AgentID:   events[0].AgentID,
		SessionID: events[0].SessionID,
		TurnID:    events[0].TurnID,
		Status:    run.StatusRunning,
		Phase:     run.PhasePrompted,
		StartedAt: events[0].Timestamp,
		UpdatedAt: events[0].Timestamp,
	}
	toolCalls := make(map[string]*run.ToolCallSnapshot)

	for _, e := range events {
		if err := updateSnapshotMetadata(s, e); err != nil {
			return nil, err
		}
		if err := applySnapshotEvent(s, toolCalls, e); err != nil {
			return nil, err
		}
	}

	if len(toolCalls) > 0 {
		s.ToolCalls = make([]*run.ToolCallSnapshot, 0, len(toolCalls))
		for _, v := range toolCalls {
			s.ToolCalls = append(s.ToolCalls, v)
		}
		sort.Slice(s.ToolCalls, func(i, j int) bool {
			a := s.ToolCalls[i]
			b := s.ToolCalls[j]
			if !a.ScheduledAt.Equal(b.ScheduledAt) {
				return a.ScheduledAt.Before(b.ScheduledAt)
			}
			return a.ToolCallID < b.ToolCallID
		})
	}

	return s, nil
}

func updateSnapshotMetadata(snapshot *run.Snapshot, event *runlog.Event) error {
	if event.RunID != snapshot.RunID {
		return fmt.Errorf("snapshot events contain multiple run IDs (%q, %q)", snapshot.RunID, event.RunID)
	}
	if snapshot.AgentID == "" && event.AgentID != "" {
		snapshot.AgentID = event.AgentID
	}
	if snapshot.SessionID == "" && event.SessionID != "" {
		snapshot.SessionID = event.SessionID
	}
	if snapshot.TurnID == "" && event.TurnID != "" {
		snapshot.TurnID = event.TurnID
	}
	if event.Timestamp.Before(snapshot.StartedAt) {
		snapshot.StartedAt = event.Timestamp
	}
	if event.Timestamp.After(snapshot.UpdatedAt) {
		snapshot.UpdatedAt = event.Timestamp
	}
	return nil
}

func applySnapshotEvent(snapshot *run.Snapshot, toolCalls map[string]*run.ToolCallSnapshot, event *runlog.Event) error {
	//nolint:exhaustive // Snapshot intentionally derives state from a small subset of events.
	switch event.Type {
	case hooks.ChildRunLinked:
		return applyChildRunLinked(snapshot, event)
	case hooks.AwaitClarification:
		return applyAwaitClarification(snapshot, event)
	case hooks.AwaitConfirmation:
		return applyAwaitConfirmation(snapshot, event)
	case hooks.AwaitExternalTools:
		return applyAwaitExternalTools(snapshot, event)
	case hooks.RunPhaseChanged:
		return applyRunPhaseChanged(snapshot, event)
	case hooks.RunResumed:
		snapshot.Await = nil
		return nil
	case hooks.AssistantMessage:
		return applyAssistantMessage(snapshot, event)
	case hooks.ToolCallScheduled:
		return applyToolCallScheduled(toolCalls, event)
	case hooks.ToolCallUpdated:
		return applyToolCallUpdated(toolCalls, event)
	case hooks.ToolResultReceived:
		return applyToolResultReceived(toolCalls, event)
	case hooks.RunCompleted:
		return applyRunCompleted(snapshot, event)
	default:
		return nil
	}
}

func applyChildRunLinked(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.ChildRunLinkedEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.ChildRuns = append(snapshot.ChildRuns, &run.ChildRunLink{
		ToolName:     payload.ToolName,
		ToolCallID:   payload.ToolCallID,
		ChildRunID:   payload.ChildRunID,
		ChildAgentID: payload.ChildAgentID,
	})
	return nil
}

func applyAwaitClarification(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.AwaitClarificationEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.Await = &run.AwaitSnapshot{
		Kind:     string(hooks.AwaitClarification),
		ID:       payload.ID,
		ToolName: payload.RestrictToTool,
		Question: payload.Question,
	}
	return nil
}

func applyAwaitConfirmation(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.AwaitConfirmationEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.Await = &run.AwaitSnapshot{
		Kind:       string(hooks.AwaitConfirmation),
		ID:         payload.ID,
		ToolName:   payload.ToolName,
		ToolCallID: payload.ToolCallID,
		Title:      payload.Title,
		Prompt:     payload.Prompt,
	}
	return nil
}

func applyAwaitExternalTools(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.AwaitExternalToolsEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.Await = &run.AwaitSnapshot{
		Kind:      string(hooks.AwaitExternalTools),
		ID:        payload.ID,
		ItemCount: len(payload.Items),
	}
	return nil
}

func applyRunPhaseChanged(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.RunPhaseChangedEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.Phase = payload.Phase
	return nil
}

func applyAssistantMessage(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload hooks.AssistantMessageEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.LastAssistantMessage = payload.Message
	return nil
}

func applyToolCallScheduled(toolCalls map[string]*run.ToolCallSnapshot, event *runlog.Event) error {
	var payload hooks.ToolCallScheduledEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	tc := getOrCreateToolCallSnapshot(toolCalls, payload.ToolCallID)
	tc.ToolName = payload.ToolName
	tc.ParentToolCallID = payload.ParentToolCallID
	if tc.ScheduledAt.IsZero() {
		tc.ScheduledAt = event.Timestamp
	}
	tc.ExpectedChildrenTotal = payload.ExpectedChildrenTotal
	if payload.ParentToolCallID != "" {
		getOrCreateToolCallSnapshot(toolCalls, payload.ParentToolCallID).ObservedChildrenTotal++
	}
	return nil
}

func applyToolCallUpdated(toolCalls map[string]*run.ToolCallSnapshot, event *runlog.Event) error {
	var payload hooks.ToolCallUpdatedEvent
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	getOrCreateToolCallSnapshot(toolCalls, payload.ToolCallID).ExpectedChildrenTotal = payload.ExpectedChildrenTotal
	return nil
}

func applyToolResultReceived(toolCalls map[string]*run.ToolCallSnapshot, event *runlog.Event) error {
	payload, err := decodeToolResultSnapshotEvent(event)
	if err != nil {
		return err
	}
	tc := getOrCreateToolCallSnapshot(toolCalls, payload.ToolCallID)
	tc.ToolName = payload.ToolName
	tc.ParentToolCallID = payload.ParentToolCallID
	tc.CompletedAt = event.Timestamp
	tc.Duration = payload.Duration
	if payload.Error != nil {
		tc.ErrorSummary = payload.Error.Message
	}
	return nil
}

func applyRunCompleted(snapshot *run.Snapshot, event *runlog.Event) error {
	var payload struct {
		Status string    `json:"status"`
		Phase  run.Phase `json:"phase"`
		Error  string    `json:"error,omitempty"`
	}
	if err := decodeSnapshotPayload(event, &payload); err != nil {
		return err
	}
	snapshot.Phase = payload.Phase
	snapshot.Await = nil
	switch payload.Status {
	case runStatusSuccess:
		snapshot.Status = run.StatusCompleted
	case runStatusFailed:
		snapshot.Status = run.StatusFailed
	case runStatusCanceled:
		snapshot.Status = run.StatusCanceled
	default:
		return fmt.Errorf("unsupported run completion status %q", payload.Status)
	}
	return nil
}

func decodeSnapshotPayload(event *runlog.Event, payload any) error {
	if err := json.Unmarshal(event.Payload, payload); err != nil {
		return fmt.Errorf("decode %s payload: %w", event.Type, err)
	}
	return nil
}

func decodeToolResultSnapshotEvent(event *runlog.Event) (*hooks.ToolResultReceivedEvent, error) {
	decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{
		Type:      hooks.ToolResultReceived,
		RunID:     event.RunID,
		AgentID:   event.AgentID,
		SessionID: event.SessionID,
		TurnID:    event.TurnID,
		Payload:   event.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("decode %s payload: %w", hooks.ToolResultReceived, err)
	}
	payload, ok := decoded.(*hooks.ToolResultReceivedEvent)
	if !ok {
		return nil, fmt.Errorf("decode %s payload: unexpected event type %T", hooks.ToolResultReceived, decoded)
	}
	return payload, nil
}

func getOrCreateToolCallSnapshot(toolCalls map[string]*run.ToolCallSnapshot, toolCallID string) *run.ToolCallSnapshot {
	tc, ok := toolCalls[toolCallID]
	if ok {
		return tc
	}
	tc = &run.ToolCallSnapshot{ToolCallID: toolCallID}
	toolCalls[toolCallID] = tc
	return tc
}
