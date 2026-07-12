package mcp

import (
	"context"
	"errors"
)

var (
	// ErrProgressReporterUnavailable is returned when a context cannot send MCP progress notifications.
	ErrProgressReporterUnavailable = errors.New("mcp progress reporter unavailable")
	// ErrProgressTokenUnavailable is returned when the active request did not supply a progress token.
	ErrProgressTokenUnavailable = errors.New("mcp progress token unavailable")
)

// ProgressUpdate describes one MCP progress notification.
type ProgressUpdate struct {
	Progress float64
	Total    float64
	Message  string
}

// ProgressReporter sends progress notifications to an MCP client.
type ProgressReporter interface {
	ReportProgress(context.Context, any, ProgressUpdate) error
}

type progressReporterKey struct{}
type progressTokenKey struct{}

// WithProgressReporter stores an MCP progress reporter in ctx.
func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

// ProgressReporterFromContext returns the MCP progress reporter stored in ctx.
func ProgressReporterFromContext(ctx context.Context) (ProgressReporter, bool) {
	if ctx == nil {
		return nil, false
	}
	reporter, ok := ctx.Value(progressReporterKey{}).(ProgressReporter)
	if !ok || reporter == nil {
		return nil, false
	}
	return reporter, true
}

// WithProgressToken stores the active MCP request's progress token in ctx.
func WithProgressToken(ctx context.Context, token any) context.Context {
	return context.WithValue(ctx, progressTokenKey{}, token)
}

// ProgressTokenFromContext returns the active MCP request's progress token.
func ProgressTokenFromContext(ctx context.Context) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	token := ctx.Value(progressTokenKey{})
	if token == nil {
		return nil, false
	}
	return token, true
}

// ReportProgress sends an update using the reporter and request token stored in ctx.
func ReportProgress(ctx context.Context, update ProgressUpdate) error {
	reporter, ok := ProgressReporterFromContext(ctx)
	if !ok {
		return ErrProgressReporterUnavailable
	}
	token, ok := ProgressTokenFromContext(ctx)
	if !ok {
		return ErrProgressTokenUnavailable
	}
	return reporter.ReportProgress(ctx, token, update)
}
