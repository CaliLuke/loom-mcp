{{ comment "Notifications and events stream" }}

func (a *MCPAdapter) NotifyStatusUpdate(ctx context.Context, n *mcpruntime.Notification) error {
    if !a.isInitialized(ctx) {
        return goa.PermanentError("invalid_params", "Not initialized")
    }
    if n == nil || n.Type == "" {
        return goa.PermanentError("invalid_params", "Missing notification type")
    }
    a.log(ctx, "request", map[string]any{
        "method": "notify_status_update",
        "type": n.Type,
        "message": n.Message,
    })
    s, err := mcpruntime.EncodeJSONToString(ctx, goahttp.ResponseEncoder, n)
    if err != nil {
        return err
    }
    ev := &EventsStreamResult{
        Content: []*ContentItem{
            buildContentItem(a, s),
        },
    }
    a.Publish(ev)
    a.log(ctx, "response", map[string]any{
        "method": "notify_status_update",
        "type": n.Type,
    })
    return nil
}

func (a *MCPAdapter) EventsStream(ctx context.Context, stream EventsStreamServerStream) error {
    if !a.isInitialized(ctx) {
        return goa.PermanentError("internal_error", "Not initialized")
    }
    a.log(ctx, "request", map[string]any{
        "method": "events/stream",
        "session_id": mcpruntime.SessionIDFromContext(ctx),
    })
    sub, err := a.broadcaster.Subscribe(ctx)
    if err != nil {
        return goa.PermanentError("internal_error", "Failed to subscribe to events: %v", err)
    }
    defer sub.Close()
    for {
        select {
        case <-ctx.Done():
            a.log(ctx, "response", map[string]any{
                "method": "events/stream",
                "session_id": mcpruntime.SessionIDFromContext(ctx),
                "closed": true,
                "reason": ctx.Err().Error(),
            })
            return ctx.Err()
        case ev, ok := <-sub.C():
            if !ok {
                a.log(ctx, "response", map[string]any{
                    "method": "events/stream",
                    "session_id": mcpruntime.SessionIDFromContext(ctx),
                    "closed": true,
                    "reason": "broadcaster_closed",
                })
                return nil
            }
            // Ensure published events implement the generated EventsStreamEvent marker.
            evt, ok := ev.(EventsStreamEvent)
            if !ok {
                a.log(ctx, "response", map[string]any{
                    "method": "events/stream",
                    "session_id": mcpruntime.SessionIDFromContext(ctx),
                    "dropped_event_type": fmt.Sprintf("%T", ev),
                })
                continue
            }
            if err := stream.Send(ctx, evt); err != nil { 
                return goa.PermanentError("internal_error", "Failed to send event: %v", err)
            }
            a.log(ctx, "response", map[string]any{
                "method": "events/stream",
                "session_id": mcpruntime.SessionIDFromContext(ctx),
                "event_type": fmt.Sprintf("%T", evt),
            })
        }
    }
}
