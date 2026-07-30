package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	goadsl "github.com/CaliLuke/loom/dsl"
)

func TestServerDataDSLRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "empty kind",
			call: func() { ServerData("", goadsl.String) },
			want: "ServerData kind must be non-empty",
		},
		{
			name: "nil schema",
			call: func() { ServerData("evidence", nil) },
			want: "ServerData(\"evidence\") requires a non-nil schema type",
		},
		{
			name: "unsupported schema argument",
			call: func() { ServerData("evidence", 42) },
			want: "cannot use 42 (type int) as type type or function",
		},
		{
			name: "empty schema type",
			call: func() { ServerData("evidence", goadsl.Empty) },
			want: "ServerData(\"evidence\") requires a schema type",
		},
		{
			name: "unsupported option",
			call: func() { ServerData("evidence", goadsl.String, 42) },
			want: "cannot use 42 (type int) as type string or function",
		},
		{
			name: "empty method result field",
			call: func() {
				ServerData("evidence", goadsl.String, func() {
					FromMethodResultField(" ")
				})
			},
			want: "FromMethodResultField requires a non-empty field name",
		},
		{
			name: "empty audience",
			call: func() {
				ServerData("evidence", goadsl.String, func() {
					Audience("")
				})
			},
			want: "Audience value must be non-empty",
		},
		{
			name: "unknown audience",
			call: func() {
				ServerData("evidence", goadsl.String, func() {
					Audience("operators")
				})
			},
			want: "Audience must be one of",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, func() {
				Toolset("tools", func() {
					Tool("lookup", "Lookup", tc.call)
				})
			}, tc.want)
		})
	}
}
