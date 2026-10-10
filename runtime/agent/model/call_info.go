package model

import "context"

// CallInfo identifies one model invocation made through a planner's model client.
// Direct Runtime.ModelClient calls do not receive this metadata automatically.
type CallInfo struct {
	AgentID string
	RunID   string
	// RootRunID is the root run's shared run store scope.
	RootRunID string
	SessionID string
	TurnID    string
	// ModelID is the registered identifier requested by the planner, which may
	// differ from the provider model named in Request.Model.
	ModelID string
	// Seq is allocated by the run store per (RootRunID, RunID), starting at 1.
	// Parallel calls and activity retries never reuse an allocated sequence.
	Seq int
	// Attempt is the engine's activity attempt, starting at 1. It is zero
	// when the caller invokes a planner activity without an engine context.
	Attempt int
}

type callInfoKey struct{}

// WithCallInfo returns a child context carrying info.
func WithCallInfo(ctx context.Context, info CallInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, info)
}

// CallInfoFrom returns the model invocation identity carried by ctx, if any.
func CallInfoFrom(ctx context.Context) (CallInfo, bool) {
	info, ok := ctx.Value(callInfoKey{}).(CallInfo)
	return info, ok
}
