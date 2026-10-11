package tools_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
)

func TestNamedIdent(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool tools.Named
	}{
		{"identifier", tools.Ident("a.b")},
		{"typed descriptor", tools.TypedTool[*string, string]{Name: "a.b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tools.Ident("a.b"), tc.tool.Ident())
		})
	}
}
