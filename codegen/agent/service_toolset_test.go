package codegen_test

import (
	"path/filepath"
	"strings"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// TestServiceToolsetIncludesMeta (legacy) – disabled.
//
// Historically this test ensured service_toolset.go included ToolCallMeta
// plumbed to the executor for method-backed tools. The generator has since
// moved to an executor-first API and this behavior is covered by registry and
// example goldens. Keeping the test skipped avoids duplicate coverage.
func TestServiceToolsetIncludesMeta(t *testing.T) {
	t.Skip("legacy test disabled: executor-first API covered by registry/example goldens")
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))

	agentsExpr.Root = &agentsExpr.RootExpr{}
	require.NoError(t, eval.Register(agentsExpr.Root))

	// Design: service with a simple method, agent with a tool bound to it.
	design := func() {
		goadsl.API("calc", func() {})
		goadsl.Service("calc", func() {
			goadsl.Method("Lookup", func() {})
			Agent("scribe", "Doc helper", func() {
				Use("lookup", func() {
					Tool("by_id", "Lookup by ID", func() {
						Args(goadsl.String)
						Return(goadsl.Boolean)
						BindTo("Lookup")
					})
				})
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	files, err := codegen.Generate("github.com/CaliLuke/loom-mcp", []eval.Root{goaexpr.Root, agentsExpr.Root}, nil)
	require.NoError(t, err)

	// Locate the service_toolset.go file and assert content mentions ToolCallMeta and meta param usage.
	var found bool
	var content string
	for _, f := range files {
		if filepath.ToSlash(f.Path) == filepath.ToSlash("gen/calc/agents/scribe/lookup/service_toolset.go") {
			found = true
			//nolint:staticcheck // Tests still inspect the legacy section list while generators migrate to Section.
			for _, s := range f.SectionTemplates {
				content += s.Source
			}
			break
		}
	}
	require.True(t, found, "expected generated service_toolset.go for method-backed toolset")
	require.Contains(t, content, "ToolCallMeta")
	hasMetaParam := strings.Contains(content, "meta ") || strings.Contains(content, ") meta,")
	require.True(t, hasMetaParam, "expected meta parameter in adapters")
}
