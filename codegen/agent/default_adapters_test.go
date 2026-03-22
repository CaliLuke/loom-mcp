package codegen_test

import (
	"bytes"
	"maps"
	"path/filepath"
	"testing"
	"text/template"

	codegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	gcodegen "github.com/CaliLuke/loom/codegen"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// Legacy: service toolset template exposed an executor-first API. Disabled to
// avoid duplicating coverage with registry/example goldens.
func TestServiceToolset_ConfigNoDefaults(t *testing.T) {
	t.Skip("legacy test disabled: executor-first API covered by registry/example goldens")
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsExpr.Root = &agentsExpr.RootExpr{}
	require.NoError(t, eval.Register(agentsExpr.Root))

	design := func() {
		API("svc", func() {})
		// Service with method that has session_id in payload and two result fields
		Service("svc", func() {
			Method("Do", func() {
				Payload(func() {
					Attribute("session_id", String)
					Attribute("q", String)
					Required("session_id")
				})
				Result(func() {
					Attribute("ok", Boolean)
					Attribute("msg", String)
					Required("ok")
				})
			})
			// Agent with a tool bound to svc.Do (within service DSL)
			Agent("a", "", func() {
				Use("ts", func() {
					Tool("do", "", func() {
						Args(String)
						Return(Boolean)
						BindTo("Do")
					})
				})
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	files, err := codegen.Generate("github.com/CaliLuke/loom-mcp", []eval.Root{goaexpr.Root, agentsExpr.Root}, nil)
	require.NoError(t, err)

	// Find generated service_toolset.go and render content
	var content string
	for _, f := range files {
		if filepath.ToSlash(f.Path) != filepath.ToSlash("gen/svc/agents/a/ts/service_toolset.go") {
			continue
		}
		var buf bytes.Buffer
		//nolint:staticcheck // Tests still inspect the legacy section list while generators migrate to Section.
		for _, s := range f.SectionTemplates {
			tmpl := template.New(s.Name).Funcs(template.FuncMap{
				"comment":     gcodegen.Comment,
				"commandLine": func() string { return "" },
			})
			if s.FuncMap != nil {
				fm := template.FuncMap{}
				maps.Copy(fm, s.FuncMap)
				tmpl = tmpl.Funcs(fm)
			}
			pt, err := tmpl.Parse(s.Source)
			require.NoError(t, err)
			var sb bytes.Buffer
			require.NoError(t, pt.Execute(&sb, s.Data))
			buf.Write(sb.Bytes())
		}
		content = buf.String()
		break
	}
	_ = content
}
