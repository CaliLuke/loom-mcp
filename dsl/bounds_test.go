package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestBoundedResultDSLConfiguresCursorFields(t *testing.T) {
	runDSL(t, func() {
		Toolset("search-tools", func() {
			Tool("search", "Search", func() {
				Args(func() {
					goadsl.Attribute("cursor", goadsl.String)
				})
				BoundedResult(func() {
					Cursor("cursor")
					NextCursor("next_cursor")
				})
			})
		})
	})

	require.Len(t, agentexpr.Root.Toolsets, 1)
	require.Len(t, agentexpr.Root.Toolsets[0].Tools, 1)
	bounds := agentexpr.Root.Toolsets[0].Tools[0].Bounds
	require.NotNil(t, bounds)
	require.NotNil(t, bounds.Paging)
	require.Equal(t, "cursor", bounds.Paging.CursorField)
	require.Equal(t, "next_cursor", bounds.Paging.NextCursorField)
}

func TestBoundedResultDSLRejectsEmptyCursorFields(t *testing.T) {
	cases := []struct {
		name string
		dsl  func()
		want string
	}{
		{
			name: "cursor",
			dsl:  func() { Cursor("") },
			want: "Cursor field name cannot be empty",
		},
		{
			name: "next cursor",
			dsl:  func() { NextCursor("") },
			want: "NextCursor field name cannot be empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, func() {
				Toolset("search-tools", func() {
					Tool("search", "Search", func() {
						BoundedResult(tc.dsl)
					})
				})
			}, tc.want)
		})
	}
}
