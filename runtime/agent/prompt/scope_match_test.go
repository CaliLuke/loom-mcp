package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeMatchesRequiresLabelPresence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{name: "nil"},
		{name: "empty", labels: map[string]string{}},
		{name: "unrelated", labels: map[string]string{"region": ""}},
		{name: "present empty", labels: map[string]string{"tenant": ""}, want: true},
		{name: "different", labels: map[string]string{"tenant": "acme"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ScopeMatches(Scope{Labels: map[string]string{"tenant": ""}}, Scope{Labels: tt.labels}))
			assert.True(t, ScopeMatches(Scope{}, Scope{Labels: tt.labels}))
		})
	}
	assert.True(t, ScopeMatches(Scope{Labels: map[string]string{"tenant": "acme"}}, Scope{Labels: map[string]string{"tenant": "acme"}}))
}

func TestInMemoryStoreEmptyLabelScope(t *testing.T) {
	for _, tt := range []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{name: "missing uses fallback", want: "global"},
		{name: "explicit empty uses override", labels: map[string]string{"tenant": ""}, want: "scoped"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewInMemoryStore()
			ctx := context.Background()
			require.NoError(t, store.Set(ctx, "p", Scope{Labels: map[string]string{"tenant": ""}}, "scoped", nil))
			scoped, err := store.Resolve(ctx, "p", Scope{Labels: tt.labels})
			require.NoError(t, err)
			if tt.labels == nil {
				assert.Nil(t, scoped)
			} else {
				require.NotNil(t, scoped)
				assert.Equal(t, "scoped", scoped.Template)
			}
			require.NoError(t, store.Set(ctx, "p", Scope{}, "global", nil))
			got, err := store.Resolve(ctx, "p", Scope{Labels: tt.labels})
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.Template)
		})
	}
}
