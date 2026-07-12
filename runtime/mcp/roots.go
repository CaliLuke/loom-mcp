package mcp

import (
	"context"
	"errors"
)

// ErrRootListerUnavailable is returned when a context cannot list MCP client roots.
var ErrRootListerUnavailable = errors.New("mcp root lister unavailable")

// Root is a filesystem boundary exposed by an MCP client.
type Root struct {
	URI  string
	Name string
}

// RootLister retrieves filesystem roots from an MCP client.
type RootLister interface {
	ListRoots(context.Context) ([]Root, error)
}

type rootListerKey struct{}

// WithRootLister stores an MCP client root lister in ctx.
func WithRootLister(ctx context.Context, lister RootLister) context.Context {
	return context.WithValue(ctx, rootListerKey{}, lister)
}

// RootListerFromContext returns the MCP client root lister stored in ctx.
func RootListerFromContext(ctx context.Context) (RootLister, bool) {
	if ctx == nil {
		return nil, false
	}
	lister, ok := ctx.Value(rootListerKey{}).(RootLister)
	if !ok || lister == nil {
		return nil, false
	}
	return lister, true
}

// ListRoots retrieves roots through the MCP client root lister stored in ctx.
func ListRoots(ctx context.Context) ([]Root, error) {
	lister, ok := RootListerFromContext(ctx)
	if !ok {
		return nil, ErrRootListerUnavailable
	}
	return lister.ListRoots(ctx)
}
