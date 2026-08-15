package codegen_test

import (
	"bytes"
	"path/filepath"
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	mcpcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/mcp"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestUnifiedToolSurfaceProjectionData(t *testing.T) {
	roots := runProjectionCodegenDesign(t)

	projected, err := mcpcodegen.ProjectedToolInventory("example.com/project/gen", roots, "assistant", "assistant-mcp")

	require.NoError(t, err)
	require.Len(t, projected, 1)
	tool := projected[0]
	require.Equal(t, "lookup_tools", tool.SourceToolset)
	require.Equal(t, "projected_lookup_tool", tool.SourceTool)
	require.Equal(t, "assistant", tool.PlacementService)
	require.Equal(t, "assistant-mcp", tool.PlacementMCPServer)
	require.Equal(t, "lookup_tools", tool.SpecsPackageName)
	require.Contains(t, tool.SpecsImportPath, "/lookup_tools")
	require.Equal(t, "assistant", tool.BoundService)
	require.Equal(t, "ProjectedLookup", tool.BoundMethod)
	require.Equal(t, "lookup_tools.projected_lookup_tool", tool.RuntimeToolName)
	require.Equal(t, "DispatchProjectedLookupToolMethod", tool.DispatcherFuncName)
	require.JSONEq(t, `{"query":"example"}`, tool.ExampleArguments)
}

func TestUnifiedToolSurfaceNoExposureCompatibility(t *testing.T) {
	roots := runNoExposureCodegenDesign(t)

	projected, err := mcpcodegen.ProjectedToolInventory("example.com/project/gen", roots, "assistant", "assistant-mcp")
	require.NoError(t, err)
	require.Empty(t, projected)

	files, err := mcpcodegen.Generate("example.com/project/gen", roots, nil)
	require.NoError(t, err)
	adapter := findProjectionGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderProjectionGeneratedFile(t, adapter)

	require.Contains(t, rendered, `"method_lookup"`)
	require.NotContains(t, rendered, "runtime_lookup_tool")
	require.NotContains(t, rendered, "DispatchRuntimeLookupToolMethod")
	require.NotContains(t, rendered, "ProjectedTool", "no-exposure generation should not introduce projected tool code")
}

func TestUnifiedToolSurfaceProjectedOnlyGeneratesToolMethods(t *testing.T) {
	roots := runProjectedOnlyJSONRPCCodegenDesign(t)

	files, err := mcpcodegen.Generate("example.com/project/gen", roots, nil)
	require.NoError(t, err)

	// The MCP service layer must define the tool surface even though the
	// design declares no method-level MCP tools: the projected toolset tool
	// is the only tool and the adapter references these types.
	service := findProjectionGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "service.go"))
	serviceSource := renderProjectionGeneratedFile(t, service)
	require.Contains(t, serviceSource, "ToolsCallPayload")
	require.Contains(t, serviceSource, "ToolsCallResult")
	require.NotContains(t, serviceSource, "ToolsListPayload")
	require.NotContains(t, serviceSource, "ToolsList(context.Context")

	adapter := findProjectionGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapterSource := renderProjectionGeneratedFile(t, adapter)
	require.Contains(t, adapterSource, `case "projected_lookup_tool":`)
	// Omitted tools/call arguments must be normalized to {} before dispatch
	// so the toolset dispatcher never receives empty raw JSON.
	require.Contains(t, adapterSource, "args := p.Arguments")
	require.Contains(t, adapterSource, `args = json.RawMessage("{}")`)
	require.Contains(t, adapterSource, "DispatchProjectedLookupToolMethod(ctx, meta, args, nil,")

	sdk := findProjectionGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "sdk_server.go"))
	sdkSource := renderProjectionGeneratedFile(t, sdk)
	require.Contains(t, sdkSource, `"projected_lookup_tool"`)
	require.Contains(t, sdkSource, "server.AddTool")
	for _, file := range files {
		require.NotContains(t, filepath.ToSlash(file.Path), "/jsonrpc/mcp_assistant/")
	}
}

func findProjectionGeneratedFile(t *testing.T, files []*gcodegen.File, path string) *gcodegen.File {
	t.Helper()
	want := filepath.ToSlash(path)
	for _, file := range files {
		if filepath.ToSlash(file.Path) == want {
			return file
		}
	}
	require.Failf(t, "generated file not found", "missing %s", want)
	return nil
}

func renderProjectionGeneratedFile(t *testing.T, file *gcodegen.File) string {
	t.Helper()
	var output bytes.Buffer
	for _, section := range file.AllSections() {
		require.NoError(t, section.Write(&output))
	}
	return output.String()
}

func runProjectionCodegenDesign(t *testing.T) []eval.Root {
	t.Helper()

	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))
	mcpexpr.Root = mcpexpr.NewRoot()
	require.NoError(t, eval.Register(mcpexpr.Root))

	design := func() {
		API("projection", func() {})
		payload := Type("LookupPayload", func() {
			Attribute("query", String)
			Required("query")
		})
		result := Type("LookupResult", func() {
			Attribute("answer", String)
			Required("answer")
		})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			JSONRPC(func() {
				POST("/rpc")
			})
			Method("projected_lookup", func() {
				Payload(payload)
				Result(result)
			})
			Agent("planner", "Planner", func() {
				Use("lookup_tools", func() {
					Tool("projected_lookup_tool", "Lookup through runtime and MCP", func() {
						Args(payload)
						Return(result)
						BindTo("assistant", "projected_lookup")
						Expose(AgentRuntime, MCPSurface)
						MCPPlacement("assistant", "assistant-mcp")
					})
				})
			})
		})
	}

	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	return prepareProjectionGeneration(t)
}

// runProjectedOnlyJSONRPCCodegenDesign evaluates a design whose MCP server has
// no method-level MCP tools: its only tool arrives via MCPPlacement projection.
// The JSON-RPC route makes the design valid for full Generate runs.
func runProjectedOnlyJSONRPCCodegenDesign(t *testing.T) []eval.Root {
	t.Helper()

	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))
	mcpexpr.Root = mcpexpr.NewRoot()
	require.NoError(t, eval.Register(mcpexpr.Root))

	design := func() {
		API("projection", func() {})
		payload := Type("LookupPayload", func() {
			Attribute("query", String)
			Required("query")
		})
		result := Type("LookupResult", func() {
			Attribute("answer", String)
			Required("answer")
		})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			JSONRPC(func() {
				POST("/rpc")
			})
			Method("projected_lookup", func() {
				Payload(payload)
				Result(result)
			})
			Agent("planner", "Planner", func() {
				Use("lookup_tools", func() {
					Tool("projected_lookup_tool", "Lookup through runtime and MCP", func() {
						Args(payload)
						Return(result)
						BindTo("assistant", "projected_lookup")
						Expose(AgentRuntime, MCPSurface)
						MCPPlacement("assistant", "assistant-mcp")
					})
				})
			})
		})
	}

	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	return prepareProjectionGeneration(t)
}

func runNoExposureCodegenDesign(t *testing.T) []eval.Root {
	t.Helper()

	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))
	mcpexpr.Root = mcpexpr.NewRoot()
	require.NoError(t, eval.Register(mcpexpr.Root))

	design := func() {
		API("projection", func() {})
		payload := Type("LookupPayload", func() {
			Attribute("query", String)
			Required("query")
		})
		result := Type("LookupResult", func() {
			Attribute("answer", String)
			Required("answer")
		})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			JSONRPC(func() {
				POST("/rpc")
			})
			Method("method_lookup", func() {
				Payload(payload)
				Result(result)
				Tool("method_lookup", "Lookup through MCP")
				JSONRPC(func() {})
			})
			Agent("planner", "Planner", func() {
				Use("lookup_tools", func() {
					Tool("runtime_lookup_tool", "Lookup through runtime only", func() {
						Args(payload)
						Return(result)
						BindTo("assistant", "method_lookup")
					})
				})
			})
		})
	}

	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	return prepareProjectionGeneration(t)
}

func prepareProjectionGeneration(t *testing.T) []eval.Root {
	t.Helper()
	roots := []eval.Root{goaexpr.Root, agentsexpr.Root, mcpexpr.Root}
	require.NoError(t, agentcodegen.Prepare("example.com/project/gen", roots))
	require.NoError(t, mcpcodegen.PrepareServices("example.com/project/gen", roots))
	return roots
}
