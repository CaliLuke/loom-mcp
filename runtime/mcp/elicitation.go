package mcp

import (
	"context"
	"errors"
)

// ErrElicitorUnavailable is returned when a context has no MCP elicitor.
var ErrElicitorUnavailable = errors.New("mcp elicitor unavailable")

// ElicitRequest describes a server-to-client MCP elicitation request.
type ElicitRequest struct {
	Mode            string
	Message         string
	RequestedSchema any
	URL             string
	ElicitationID   string
}

// ElicitResult describes the client's response to an elicitation request.
type ElicitResult struct {
	Action  string
	Content map[string]any
}

// Elicitor requests user input from an MCP client.
type Elicitor interface {
	Elicit(context.Context, ElicitRequest) (*ElicitResult, error)
}

type elicitorKey struct{}

// WithElicitor stores an MCP elicitor in ctx.
func WithElicitor(ctx context.Context, elicitor Elicitor) context.Context {
	return context.WithValue(ctx, elicitorKey{}, elicitor)
}

// ElicitorFromContext returns the MCP elicitor stored in ctx.
func ElicitorFromContext(ctx context.Context) (Elicitor, bool) {
	if ctx == nil {
		return nil, false
	}
	elicitor, ok := ctx.Value(elicitorKey{}).(Elicitor)
	if !ok || elicitor == nil {
		return nil, false
	}
	return elicitor, true
}

// Elicit requests user input through the MCP elicitor stored in ctx.
func Elicit(ctx context.Context, req ElicitRequest) (*ElicitResult, error) {
	elicitor, ok := ElicitorFromContext(ctx)
	if !ok {
		return nil, ErrElicitorUnavailable
	}
	return elicitor.Elicit(ctx, req)
}
