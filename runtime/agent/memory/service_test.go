package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScopeResolverFunc(t *testing.T) {
	resolver := ScopeResolverFunc(func(_ context.Context, input ScopeInput) (Scope, error) {
		require.Equal(t, "svc.agent", input.AgentID)
		return Scope{Namespace: "agent:svc.agent", UserID: "user-1", Visibility: VisibilityUser}, nil
	})

	scope, err := resolver.ResolveMemoryScope(context.Background(), ScopeInput{AgentID: "svc.agent"})
	require.NoError(t, err)
	require.Equal(t, Scope{Namespace: "agent:svc.agent", UserID: "user-1", Visibility: VisibilityUser}, scope)
}
