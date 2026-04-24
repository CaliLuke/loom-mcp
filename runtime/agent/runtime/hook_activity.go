package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
	rthints "github.com/CaliLuke/loom-mcp/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
)

// hookActivityName is the engine-registered activity that publishes hook events
// on behalf of workflow code.
const hookActivityName = "runtime.publish_hook"

// hookActivity publishes workflow-emitted hook events outside of deterministic
// workflow execution.
//
// Contract:
//   - The canonical record of runtime events is the run event log. Appending to
//     RunEventStore is a correctness invariant: failures must fail the activity
//     so the workflow run can stop and/or be retried by the engine.
//   - Streaming is a best-effort session projection:
//   - While the session is active, stream emission failures are logged/traced
//     but must not fail the activity or corrupt the canonical run record.
//   - After the session is ended, stream emission becomes a no-op to avoid
//     "stream destroyed mid-run" turning into spurious run failures.
//   - One-shot runs (empty SessionID) bypass SessionStore and stream sinks.
//   - Publishing to the hook bus is best-effort. The bus drives derived storage
//     (memory) and local observability, but it must not be allowed to corrupt or
//     block the canonical transcript.
func (r *Runtime) hookActivity(ctx context.Context, input *HookActivityInput) error {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	evt, payload, err := r.decodeHookActivityEvent(ctx, input)
	if err != nil {
		return err
	}
	// Tool call argument deltas are best-effort UX signals. They are intentionally
	// excluded from the canonical run event log to avoid bloating durable history.
	//
	// Consumers must treat ToolCallArgsDelta as optional; the canonical tool
	// payload is still emitted via tool_start/tool_end and the finalized tool call.
	if input.Type != hooks.ToolCallArgsDelta {
		if err := r.appendHookRunEvent(ctx, input, evt, payload); err != nil {
			return err
		}
		if err := r.updateHookRunMeta(ctx, input.SessionID, evt); err != nil {
			return err
		}
	}
	r.publishHookStreamEvent(ctx, input.SessionID, evt)
	if err := r.publishHookBusEvent(ctx, input.Type, evt); err != nil {
		return err
	}
	if input.Type == hooks.RunCompleted {
		r.storeWorkflowHandle(input.RunID, nil)
	}
	return nil
}

func (r *Runtime) decodeHookActivityEvent(ctx context.Context, input *HookActivityInput) (hooks.Event, []byte, error) {
	evt, err := hooks.DecodeFromHookInput(input)
	if err != nil {
		return nil, nil, err
	}
	payload := append([]byte(nil), input.Payload...)
	if e, ok := evt.(*hooks.ToolCallScheduledEvent); ok && r.enrichToolCallScheduledHint(ctx, e) {
		reencoded, err := hooks.EncodeToHookInput(e, input.TurnID)
		if err == nil {
			payload = append([]byte(nil), reencoded.Payload.RawMessage()...)
		}
	}
	return evt, payload, nil
}

func (r *Runtime) appendHookRunEvent(ctx context.Context, input *HookActivityInput, evt hooks.Event, payload []byte) error {
	_, err := r.RunEventStore.Append(ctx, &runlog.Event{
		EventKey:  input.EventKey,
		RunID:     input.RunID,
		AgentID:   input.AgentID,
		SessionID: input.SessionID,
		TurnID:    input.TurnID,
		Type:      input.Type,
		Payload:   payload,
		Timestamp: time.UnixMilli(evt.Timestamp()).UTC(),
	})
	return err
}

func (r *Runtime) updateHookRunMeta(ctx context.Context, sessionID string, evt hooks.Event) error {
	if sessionID == "" {
		return nil
	}
	return r.updateRunMetaFromHookEvent(ctx, evt)
}

func (r *Runtime) publishHookStreamEvent(ctx context.Context, sessionID string, evt hooks.Event) {
	if sessionID == "" || r.streamSubscriber == nil {
		return
	}
	sess, err := r.SessionStore.LoadSession(ctx, sessionID)
	if err != nil {
		r.logWarn(ctx, "stream session lookup failed", err, "session_id", sessionID, "event", string(evt.Type()))
		return
	}
	if sess.Status == session.StatusEnded {
		return
	}
	if err := r.streamSubscriber.HandleEvent(ctx, evt); err != nil {
		r.logWarn(ctx, "stream subscriber failed", err, "session_id", sessionID, "event", string(evt.Type()))
	}
}

// publishHookBusEvent forwards evt to every bus subscriber and returns the
// first propagated error. Best-effort subscribers installed via
// registerSubscriber have their errors swallowed inside the wrapper, so any
// returned error here came from a critical subscriber.
func (r *Runtime) publishHookBusEvent(ctx context.Context, eventType hooks.EventType, evt hooks.Event) error {
	if eventType == hooks.ToolCallArgsDelta {
		return nil
	}
	if err := r.Bus.Publish(ctx, evt); err != nil {
		r.logWarn(ctx, "hook publish failed", err, "event", evt.Type())
		return err
	}
	return nil
}

func (r *Runtime) enrichToolCallScheduledHint(ctx context.Context, evt *hooks.ToolCallScheduledEvent) bool {
	if evt == nil {
		return false
	}
	if evt.DisplayHint != "" {
		return false
	}
	raw := normalizeHintPayloadJSON(evt.Payload.RawMessage())
	typed, err := r.unmarshalToolValue(ctx, evt.ToolName, raw, true)
	if err != nil || typed == nil {
		return false
	}
	if hint := rthints.FormatCallHint(evt.ToolName, typed); hint != "" {
		evt.DisplayHint = hint
		return true
	}
	return false
}

func normalizeHintPayloadJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage("{}")
	}
	return raw
}

func (r *Runtime) updateRunMetaFromHookEvent(ctx context.Context, evt hooks.Event) error {
	if evt == nil {
		return errors.New("runtime: hook event is nil")
	}
	switch e := evt.(type) {
	case *hooks.RunStartedEvent:
		return r.updateRunStartedMeta(ctx, e)
	case *hooks.PromptRenderedEvent:
		return r.updatePromptRenderedMeta(ctx, e)
	case *hooks.ChildRunLinkedEvent:
		return r.SessionStore.LinkChildRun(ctx, e.RunID(), session.RunMeta{
			AgentID:   string(e.ChildAgentID),
			RunID:     e.ChildRunID,
			SessionID: e.SessionID(),
			Status:    session.RunStatusPending,
		})
	case *hooks.RunPausedEvent:
		return r.updateRunStatus(ctx, e.RunID(), session.RunStatusPaused)
	case *hooks.RunResumedEvent:
		return r.updateRunStatus(ctx, e.RunID(), session.RunStatusRunning)
	case *hooks.RunCompletedEvent:
		switch e.Status {
		case "success":
			return r.updateRunStatus(ctx, e.RunID(), session.RunStatusCompleted)
		case "failed":
			return r.updateRunStatus(ctx, e.RunID(), session.RunStatusFailed)
		case "canceled":
			return r.updateRunStatus(ctx, e.RunID(), session.RunStatusCanceled)
		default:
			return errors.New("runtime: run completed event has unknown status")
		}
	default:
		return nil
	}
}

func (r *Runtime) updateRunStartedMeta(ctx context.Context, e *hooks.RunStartedEvent) error {
	run, err := r.SessionStore.LoadRun(ctx, e.RunID())
	if err != nil {
		if errors.Is(err, session.ErrRunNotFound) {
			startedAt := time.UnixMilli(e.Timestamp()).UTC()
			now := time.Now().UTC()
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   e.AgentID(),
				RunID:     e.RunID(),
				SessionID: e.SessionID(),
				Status:    session.RunStatusRunning,
				StartedAt: startedAt,
				UpdatedAt: now,
				Labels:    cloneLabels(e.RunContext.Labels),
			})
		}
		return err
	}
	run.Status = session.RunStatusRunning
	run.UpdatedAt = time.Now().UTC()
	run.Labels = cloneLabels(e.RunContext.Labels)
	return r.SessionStore.UpsertRun(ctx, run)
}

func (r *Runtime) updatePromptRenderedMeta(ctx context.Context, e *hooks.PromptRenderedEvent) error {
	run, err := r.SessionStore.LoadRun(ctx, e.RunID())
	if err != nil {
		if errors.Is(err, session.ErrRunNotFound) {
			now := time.Now().UTC()
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   e.AgentID(),
				RunID:     e.RunID(),
				SessionID: e.SessionID(),
				Status:    session.RunStatusRunning,
				StartedAt: time.UnixMilli(e.Timestamp()).UTC(),
				UpdatedAt: now,
				PromptRefs: []prompt.PromptRef{
					{ID: e.PromptID, Version: e.Version},
				},
			})
		}
		return err
	}
	run.UpdatedAt = time.Now().UTC()
	run.PromptRefs = appendUniquePromptRef(run.PromptRefs, prompt.PromptRef{
		ID:      e.PromptID,
		Version: e.Version,
	})
	return r.SessionStore.UpsertRun(ctx, run)
}

func (r *Runtime) updateRunStatus(ctx context.Context, runID string, status session.RunStatus) error {
	run, err := r.SessionStore.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = status
	run.UpdatedAt = time.Now().UTC()
	return r.SessionStore.UpsertRun(ctx, run)
}

// appendUniquePromptRef appends ref only when it is not already present.
// Uniqueness is defined by (prompt_id, version) and ordering is first-seen.
func appendUniquePromptRef(existing []prompt.PromptRef, ref prompt.PromptRef) []prompt.PromptRef {
	for _, cur := range existing {
		if cur.ID == ref.ID && cur.Version == ref.Version {
			return existing
		}
	}
	return append(existing, ref)
}
