package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterRateLimitPreservesFractionalSeed(t *testing.T) {
	for _, tt := range []struct {
		name    string
		initial float64
		want    string
	}{
		{"fractional", 1.5, "1.5"},
		{"integer", 100, "100"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newFakeClusterMap()
			limiter := newClusterAdaptiveRateLimiter(t.Context(), m, "key", tt.initial, 200)
			limiter.mu.Lock()
			actual := limiter.currentTPM
			limiter.mu.Unlock()
			assert.InDelta(t, tt.initial, actual, 0)
			value, ok := m.Get("key")
			require.True(t, ok)
			assert.Equal(t, tt.want, value)
		})
	}
}

func TestClusterRateLimitPreservesFractionalUpdates(t *testing.T) {
	for _, tt := range []struct {
		name    string
		initial string
		update  func(*testing.T, *fakeClusterMap)
		want    string
	}{
		{"backoff", "1.5", func(t *testing.T, m *fakeClusterMap) {
			globalBackoff(t.Context(), m, "key", 0.5)
		}, "0.75"},
		{"floor", "1.5", func(t *testing.T, m *fakeClusterMap) {
			globalBackoff(t.Context(), m, "key", 1.25)
		}, "1.25"},
		{"probe", "1.5", func(t *testing.T, m *fakeClusterMap) {
			globalProbe(t.Context(), m, "key", 0.25, 2.5)
		}, "1.75"},
		{"ceiling", "2.25", func(t *testing.T, m *fakeClusterMap) {
			globalProbe(t.Context(), m, "key", 0.5, 2.5)
		}, "2.5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newFakeClusterMap()
			m.values["key"] = tt.initial
			tt.update(t, m)
			value, ok := m.Get("key")
			require.True(t, ok)
			assert.Equal(t, tt.want, value)
		})
	}
}
