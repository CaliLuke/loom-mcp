package runtime

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
)

// publishModelPresentation sends provisional model output directly to the
// configured stream. It deliberately bypasses hooks, memory, and the durable
// run log.
func (r *Runtime) publishModelPresentation(ctx context.Context, sessionID string, event stream.Event) {
	if sessionID == "" || r.streamSubscriber == nil {
		return
	}
	if r.streamSessionEnded(sessionID) {
		return
	}
	sess, err := r.SessionStore.LoadSession(ctx, sessionID)
	if err != nil {
		r.logWarn(ctx, "model presentation session lookup failed", err, "session_id", sessionID, "event", string(event.Type()))
		return
	}
	if sess.Status == session.StatusEnded {
		r.markStreamSessionEnded(sessionID)
		return
	}
	if err := r.streamSubscriber.HandleProvisionalEvent(ctx, event); err != nil {
		r.logWarn(ctx, "model presentation stream failed", err, "session_id", sessionID, "event", string(event.Type()))
	}
}
