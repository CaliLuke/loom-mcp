package codegen_test

import (
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// Ensures external MCP toolsets are materialized as self-contained types (no aliases).
func TestExternalMCPToolset_SelfContainedTypes(t *testing.T) {
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsExpr.Root = &agentsExpr.RootExpr{}
	require.NoError(t, eval.Register(agentsExpr.Root))

	design := func() {
		API("svc", func() {})
		// External MCP providers define their model-facing schemas inline.
		Service("assistant", func() {})
		assistantSuite := Toolset(FromMCP("assistant", "assistant-mcp"), func() {
			Tool("lookup", "Look up a value", func() {
				Args(func() {
					Attribute("query", String)
				})
				Return(func() {
					Attribute("result", String)
				})
			})
		})
		Service("svc", func() {
			Agent("a", "", func() {
				Use(assistantSuite)
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	data, err := codegen.BuildDataForTest("github.com/CaliLuke/loom-mcp/v2", []eval.Root{goaexpr.Root, agentsExpr.Root})
	require.NoError(t, err)
	require.NotNil(t, data)
	svc := data.Services[0]
	ag := svc.Agents[0]
	specs, err := codegen.BuildToolSpecsDataForTest(ag)
	require.NoError(t, err)

	defs := codegen.CollectTypeInfoForTest(specs)
	require.NotEmpty(t, defs, "expected generated external MCP tool types")
	for name, def := range defs {
		require.Containsf(t, def, " struct {", "expected self-contained type in %s: %s", name, def)
	}
}
