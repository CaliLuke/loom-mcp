package codegen

import (
	"testing"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// Ensures CLI patching and adapter stub generation work for multiple MCP-enabled services.
func TestMultiService_GeneratesCLIAndStubs(t *testing.T) {
	// Two services with one method each
	alpha := &expr.ServiceExpr{Name: "Alpha", Methods: []*expr.MethodExpr{{Name: "One"}}}
	beta := &expr.ServiceExpr{Name: "Beta", Methods: []*expr.MethodExpr{{Name: "Two"}}}

	// Server referencing both services
	svr := &expr.ServerExpr{Name: "orchestrator", Services: []string{"Alpha", "Beta"}}

	// CLI file for server
	cliHeader := &codegen.SectionTemplate{
		Name: headerSection,
		Data: map[string]any{
			"Imports": []*codegen.ImportSpec{
				{Path: "example.com/assistant/gen/jsonrpc/cli/orchestrator/cli"},
			},
		},
	}
	cliStart := &codegen.SectionTemplate{Name: "cli-http-start", Source: "// original"}
	cliEnd := &codegen.SectionTemplate{Name: "cli-http-end", Source: "// original end"}
	cliFile := &codegen.File{
		Path:     "cmd/orchestrator-cli/jsonrpc.go",
		Sections: []codegen.Section{cliHeader, cliStart, cliEnd},
	}

	// Existing example stubs (to be replaced)
	alphaHeader := &codegen.SectionTemplate{Name: headerSection, Data: map[string]any{
		"Imports": []*codegen.ImportSpec{{Path: "example.com/assistant/gen/mcp_alpha", Name: "mcpalpha"}},
	}}
	alphaBody := &codegen.SectionTemplate{
		Name:   "body",
		Source: "func NewMcpAlpha() mcpalpha.Service { return &mcpAlphasrvc{} }",
	}
	alphaStub := &codegen.File{
		Path:     "mcp_alpha.go",
		Sections: []codegen.Section{alphaHeader, alphaBody},
	}

	betaHeader := &codegen.SectionTemplate{Name: headerSection, Data: map[string]any{
		"Imports": []*codegen.ImportSpec{{Path: "example.com/assistant/gen/mcp_beta", Name: "mcpbeta"}},
	}}
	betaBody := &codegen.SectionTemplate{
		Name:   "body",
		Source: "func NewMcpBeta() mcpbeta.Service { return &mcpBetasrvc{} }",
	}
	betaStub := &codegen.File{Path: "mcp_beta.go", Sections: []codegen.Section{betaHeader, betaBody}}

	files := []*codegen.File{cliFile, alphaStub, betaStub}

	// Patch CLI to use adapter clients for both services
	files, err := patchCLIForServer("orchestrator", svr, []*expr.ServiceExpr{alpha, beta}, files)
	require.NoError(t, err)

	// Generate adapter stubs for both services and replace bodies
	_, err = generateExampleAdapterStubs("example.com/assistant/gen", []*expr.ServiceExpr{alpha, beta}, files)
	require.NoError(t, err)

	// Validate CLI header contains both adapter client imports
	var importPaths []string
	if data, ok := cliHeader.Data.(map[string]any); ok {
		if imv, ok2 := data["Imports"]; ok2 {
			if specs, ok3 := imv.([]*codegen.ImportSpec); ok3 {
				for _, s := range specs {
					importPaths = append(importPaths, s.Path)
				}
			}
		}
	}
	require.Contains(t, importPaths, "example.com/assistant/gen/mcp_alpha/adapter/client")
	require.Contains(t, importPaths, "example.com/assistant/gen/mcp_beta/adapter/client")
	require.Len(t, cliFile.Section("cli-dojsonrpc"), 1)
	require.Empty(t, cliFile.Section("cli-http-start"))
	require.Empty(t, cliFile.Section("cli-http-end"))

	// Validate stubs were replaced with template section
	require.Len(t, alphaStub.Section(exampleMCPStubSection), 1)
	require.Len(t, betaStub.Section(exampleMCPStubSection), 1)
}
