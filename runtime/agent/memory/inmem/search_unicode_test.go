package inmem_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
)

func TestServiceSearchUnicodeTerms(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		query   string
		matches bool
	}{
		{"Japanese", "東京の天気", "東京", true},
		{"Chinese", "北京天气晴朗", "北京", true},
		{"accented_case", "CAFÉ noir", "café", true},
		{"combining_mark", "cafe\u0301 noir", "CAFE\u0301", true},
		{"combining_mark_is_not_discarded", "cafe noir", "cafe\u0301", false},
		{"Unicode_digits", "番号１２３", "１２３", true},
		{"Unicode_punctuation", "東京の天気", "東京、天気！", true},
		{"ASCII_punctuation", "Release checklist", "RELEASE,checklist!", true},
		{"no_implicit_normalization", "cafe\u0301 noir", "café", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := inmem.NewService()
			scope := memory.Scope{Namespace: "workspace", UserID: "user", Visibility: memory.VisibilityUser}
			entry, err := service.PutEntry(t.Context(), memory.PutEntryInput{Scope: scope, Content: tc.content})
			require.NoError(t, err)
			_, err = service.PutEntry(t.Context(), memory.PutEntryInput{Scope: scope, Content: "unrelated banana"})
			require.NoError(t, err)

			result, err := service.Search(t.Context(), memory.SearchQuery{Scope: scope, Query: tc.query})

			require.NoError(t, err)
			assert.False(t, result.Truncated)
			if !tc.matches {
				assert.Empty(t, result.Hits)
				return
			}
			require.Len(t, result.Hits, 1)
			assert.Equal(t, entry.ID, result.Hits[0].Entry.ID)
			assert.InDelta(t, 1.0, result.Hits[0].Score, 1e-12)
		})
	}
}

func TestServiceSearchDistinguishesBrowseFromUnsearchableQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		count int
	}{
		{"empty_browses", "", 1},
		{"whitespace_browses", " \t\n\u3000", 1},
		{"ASCII_punctuation", "?!_-", 0},
		{"Unicode_punctuation", "。！？", 0},
		{"symbols", "🌧️", 0},
		{"mark_only", "\u0301", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := inmem.NewService()
			scope := memory.Scope{Namespace: "workspace", UserID: "user", Visibility: memory.VisibilityUser}
			_, err := service.PutEntry(t.Context(), memory.PutEntryInput{Scope: scope, Content: "retained memory ☀️ cafe\u0301"})
			require.NoError(t, err)

			result, err := service.Search(t.Context(), memory.SearchQuery{Scope: scope, Query: tc.query})

			require.NoError(t, err)
			assert.Len(t, result.Hits, tc.count)
			assert.False(t, result.Truncated)
		})
	}
}

func TestServiceUnicodeSearchPreservesFiltersRankingAndLimit(t *testing.T) {
	service := inmem.NewService()
	scope := memory.Scope{Namespace: "workspace", UserID: "user", Visibility: memory.VisibilityUser}
	for _, entry := range []memory.PutEntryInput{
		{Scope: scope, Content: "東京の天気", Timestamp: time.Unix(10, 0), Labels: map[string]string{"topic": "weather"}},
		{Scope: scope, Content: "東京", Timestamp: time.Unix(20, 0), Labels: map[string]string{"topic": "weather"}},
		{Scope: scope, Content: "天気", Timestamp: time.Unix(15, 0), Labels: map[string]string{"topic": "weather"}},
		{Scope: scope, Content: "東京 天気 wrong label", Timestamp: time.Unix(30, 0), Labels: map[string]string{"topic": "other"}},
		{Scope: memory.Scope{Namespace: "other", UserID: "user", Visibility: memory.VisibilityUser}, Content: "東京 天気 wrong namespace", Labels: map[string]string{"topic": "weather"}},
		{Scope: memory.Scope{Namespace: "workspace", UserID: "other", Visibility: memory.VisibilityUser}, Content: "東京 天気 wrong user", Labels: map[string]string{"topic": "weather"}},
		{Scope: memory.Scope{Namespace: "workspace", Visibility: memory.VisibilityShared}, Content: "東京 天気 shared", Labels: map[string]string{"topic": "weather"}},
	} {
		_, err := service.PutEntry(t.Context(), entry)
		require.NoError(t, err)
	}

	result, err := service.Search(t.Context(), memory.SearchQuery{Scope: scope, Query: "東京、天気", Labels: map[string]string{"topic": "weather"}, Limit: 2})

	require.NoError(t, err)
	require.Len(t, result.Hits, 2)
	assert.Equal(t, "東京の天気", result.Hits[0].Entry.Content)
	assert.InDelta(t, 1.0, result.Hits[0].Score, 1e-12)
	assert.Equal(t, "東京", result.Hits[1].Entry.Content)
	assert.InDelta(t, 0.5, result.Hits[1].Score, 1e-12)
	assert.True(t, result.Truncated)
}
