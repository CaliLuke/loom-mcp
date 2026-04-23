package codegen_test

import (
	"path/filepath"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestGeneratedAgentUnionUsesExplicitVariantTags(t *testing.T) {
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))

	agentsExpr.Root = &agentsExpr.RootExpr{}
	require.NoError(t, eval.Register(agentsExpr.Root))

	design := func() {
		goadsl.API("alpha", func() {})

		var ListPayload = goadsl.Type("ListPayload", func() {
			goadsl.Attribute("limit", goadsl.Int, "Maximum number of items")
		})
		var GetActivePayload = goadsl.Type("GetActivePayload", func() {
		})
		var Request = goadsl.Type("Request", func() {
			goadsl.OneOf("value", func() {
				goadsl.Attribute("list_branch", ListPayload, func() {
					goadsl.Meta("oneof:type:tag", "list")
				})
				goadsl.Attribute("get_active_branch", GetActivePayload, func() {
					goadsl.Meta("oneof:type:tag", "get_active")
				})
			})
			goadsl.Required("value")
		})

		goadsl.Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("ops", func() {
					Tool("dispatch", "Dispatch", func() {
						Args(Request)
						Return(goadsl.String)
					})
				})
			})
		})
	}

	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	files, err := codegen.Generate("github.com/CaliLuke/loom-mcp", []eval.Root{goaexpr.Root, agentsExpr.Root}, nil)
	require.NoError(t, err)

	unions := testhelpers.FileContent(t, files, filepath.ToSlash("gen/alpha/toolsets/ops/unions.go"))
	require.NotEmpty(t, unions)
	require.Contains(t, unions, `= "list"`)
	require.Contains(t, unions, `= "get_active"`)
	require.NotContains(t, unions, `= "ListPayload"`)
	require.NotContains(t, unions, `= "GetActivePayload"`)
	require.Contains(t, unions, `case string(`)
	require.Contains(t, unions, "get_active")
}
