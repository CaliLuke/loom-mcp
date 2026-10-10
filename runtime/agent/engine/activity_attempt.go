package engine

import "context"

type activityAttemptKey struct{}

// WithActivityAttempt returns a child context carrying the current activity's
// one-based attempt. Engine adapters set it on each activity invocation.
func WithActivityAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, activityAttemptKey{}, attempt)
}

// ActivityAttemptFromContext returns the engine's current activity attempt.
// Attempts start at 1; zero means no engine activity attempt was attached.
func ActivityAttemptFromContext(ctx context.Context) int {
	attempt, ok := ctx.Value(activityAttemptKey{}).(int)
	if !ok {
		return 0
	}
	return attempt
}
