package toolregistry

import "context"

type toolUseIDContextKey struct{}

// WithToolUseID returns a context carrying the canonical run-scoped tool-call identity.
func WithToolUseID(ctx context.Context, toolUseID string) context.Context {
	return context.WithValue(ctx, toolUseIDContextKey{}, toolUseID)
}

// ToolUseIDFromContext returns the canonical run-scoped tool-call identity.
func ToolUseIDFromContext(ctx context.Context) (string, bool) {
	toolUseID, ok := ctx.Value(toolUseIDContextKey{}).(string)
	return toolUseID, ok
}
