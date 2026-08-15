package codegen

import (
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestPrepareExampleValidatesMCPMappingsWithoutAddingTransport(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "search", "unmapped")
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name: "assistant", Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{{Name: "search", Method: methods["search"]}},
	})

	err := PrepareExample("example.com/assistant/gen", []eval.Root{root})
	require.ErrorContains(t, err, "methods not mapped to MCP constructs: unmapped")
}

func TestModifyExampleFilesLeavesApplicationExamplesUntouched(t *testing.T) {
	files := []*codegen.File{{Path: "cmd/server/main.go"}}

	got, err := ModifyExampleFiles("example.com/assistant/gen", nil, files)

	require.NoError(t, err)
	require.Equal(t, files, got)
}
