package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListRootsUsesContextRootLister(t *testing.T) {
	want := []Root{{URI: "file:///workspace", Name: "Workspace"}}
	lister := rootListerFunc(func(context.Context) ([]Root, error) {
		return want, nil
	})

	result, err := ListRoots(WithRootLister(context.Background(), lister))

	require.NoError(t, err)
	require.Equal(t, want, result)
}

func TestListRootsReturnsUnavailableWithoutContextRootLister(t *testing.T) {
	_, err := ListRoots(context.Background())

	require.ErrorIs(t, err, ErrRootListerUnavailable)
}

func TestListRootsReturnsListerErrors(t *testing.T) {
	wantErr := errors.New("client rejected roots/list")
	lister := rootListerFunc(func(context.Context) ([]Root, error) {
		return nil, wantErr
	})

	_, err := ListRoots(WithRootLister(context.Background(), lister))

	require.ErrorIs(t, err, wantErr)
}

type rootListerFunc func(context.Context) ([]Root, error)

func (f rootListerFunc) ListRoots(ctx context.Context) ([]Root, error) {
	return f(ctx)
}
