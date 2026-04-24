package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/runtime/agent/stream"
)

func (r *Runtime) installRuntimeSubscribers(bus hooks.Bus) {
	r.mu.Lock()
	r.addToolsetLocked(toolUnavailableToolsetRegistration())
	r.mu.Unlock()
	if r.SessionStore != nil {
		r.registerSessionSubscriber(bus)
	}
	if r.Memory != nil {
		r.registerMemorySubscriber(bus)
	}
	if r.Stream != nil {
		streamSub, err := stream.NewSubscriber(newHintingSink(r, r.Stream))
		if err != nil {
			r.logger.Warn(context.Background(), "failed to create stream subscriber", "err", err)
		} else {
			r.streamSubscriber = streamSub
		}
	}
}

func (r *Runtime) registerSessionSubscriber(bus hooks.Bus) {
	sessionSub := hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		if event.SessionID() == "" {
			return nil
		}
		meta, ok, err := sessionRunMetaFromEvent(event)
		if err != nil || !ok {
			return err
		}
		return r.SessionStore.UpsertRun(ctx, meta)
	})
	if _, err := r.registerSubscriber(bus, sessionSub, SubscriberBestEffort); err != nil {
		r.logger.Warn(context.Background(), "failed to register session subscriber", "err", err)
	}
}

func (r *Runtime) registerMemorySubscriber(bus hooks.Bus) {
	memSub := hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		agentID, runID, memEvent, ok := projectMemoryEvent(event)
		if !ok {
			return nil
		}
		return r.Memory.AppendEvents(ctx, agentID, runID, memEvent)
	})
	if _, err := r.registerSubscriber(bus, memSub, SubscriberBestEffort); err != nil {
		r.logger.Warn(context.Background(), "failed to register memory subscriber", "err", err)
	}
}

func sessionRunMetaFromEvent(event hooks.Event) (session.RunMeta, bool, error) {
	ts := time.UnixMilli(event.Timestamp()).UTC()
	switch evt := event.(type) {
	case *hooks.RunStartedEvent:
		return session.RunMeta{
			AgentID:   evt.AgentID(),
			RunID:     evt.RunID(),
			SessionID: evt.SessionID(),
			Status:    session.RunStatusRunning,
			UpdatedAt: ts,
			Labels:    evt.RunContext.Labels,
			StartedAt: time.Time{},
		}, true, nil
	case *hooks.RunPausedEvent:
		return session.RunMeta{
			AgentID:   evt.AgentID(),
			RunID:     evt.RunID(),
			SessionID: evt.SessionID(),
			Status:    session.RunStatusPaused,
			UpdatedAt: ts,
			Labels:    evt.Labels,
			Metadata:  evt.Metadata,
		}, true, nil
	case *hooks.RunResumedEvent:
		return session.RunMeta{
			AgentID:   evt.AgentID(),
			RunID:     evt.RunID(),
			SessionID: evt.SessionID(),
			Status:    session.RunStatusRunning,
			UpdatedAt: ts,
			Labels:    evt.Labels,
		}, true, nil
	case *hooks.RunCompletedEvent:
		status, err := sessionRunCompletedStatus(evt.Status)
		if err != nil {
			return session.RunMeta{}, false, err
		}
		return session.RunMeta{
			AgentID:   evt.AgentID(),
			RunID:     evt.RunID(),
			SessionID: evt.SessionID(),
			Status:    status,
			UpdatedAt: ts,
			Metadata:  sessionRunCompletedMetadata(evt),
		}, true, nil
	default:
		return session.RunMeta{}, false, nil
	}
}

func sessionRunCompletedStatus(status string) (session.RunStatus, error) {
	switch status {
	case "success":
		return session.RunStatusCompleted, nil
	case "failed":
		return session.RunStatusFailed, nil
	case "canceled":
		return session.RunStatusCanceled, nil
	default:
		return "", fmt.Errorf("unexpected run completed status %q", status)
	}
}

func sessionRunCompletedMetadata(evt *hooks.RunCompletedEvent) map[string]any {
	if evt.PublicError == "" {
		return nil
	}
	metadata := map[string]any{
		"public_error": evt.PublicError,
		"retryable":    evt.Retryable,
	}
	if evt.ErrorProvider != "" {
		metadata["error_provider"] = evt.ErrorProvider
	}
	if evt.ErrorOperation != "" {
		metadata["error_operation"] = evt.ErrorOperation
	}
	if evt.ErrorKind != "" {
		metadata["error_kind"] = evt.ErrorKind
	}
	if evt.ErrorCode != "" {
		metadata["error_code"] = evt.ErrorCode
	}
	if evt.HTTPStatus != 0 {
		metadata["http_status"] = evt.HTTPStatus
	}
	return metadata
}
