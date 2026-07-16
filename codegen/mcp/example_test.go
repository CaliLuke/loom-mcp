package codegen

import (
	"bytes"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/require"
)

func TestPatchCLIForServer_UsesMCPAdapterClient(t *testing.T) {
	// Arrange: CLI file with header and start sections
	header := &codegen.SectionTemplate{
		Name: headerSection,
		Data: map[string]any{
			"Imports": []*codegen.ImportSpec{
				{Path: "example.com/assistant/gen/jsonrpc/cli/orchestrator/cli"},
			},
		},
	}
	start := &codegen.SectionTemplate{Name: "cli-http-start", Source: "// original"}
	end := &codegen.SectionTemplate{Name: "cli-http-end", Source: "// original end"}
	cliFile := &codegen.File{
		Path:     "cmd/orchestrator-cli/jsonrpc.go",
		Sections: []codegen.Section{header, start, end},
	}

	// One MCP-enabled service with one method
	svc := &expr.ServiceExpr{Name: "orchestrator", Methods: []*expr.MethodExpr{{Name: "EventsStream"}}}
	svr := &expr.ServerExpr{Name: "srv", Services: []string{"orchestrator"}}

	files, err := patchCLIForServer("orchestrator", svr, []*expr.ServiceExpr{svc}, []*codegen.File{cliFile})
	require.NoError(t, err)
	require.Len(t, files, 1)

	// Assert: header now imports the MCP adapter client and start section is replaced
	var hasAdapterImport bool
	if data, ok := header.Data.(map[string]any); ok {
		if imv, ok2 := data["Imports"]; ok2 {
			if specs, ok3 := imv.([]*codegen.ImportSpec); ok3 {
				for _, s := range specs {
					if s.Path == "example.com/assistant/gen/mcp_orchestrator/adapter/client" {
						hasAdapterImport = true
						break
					}
				}
			}
		}
	}
	require.True(t, hasAdapterImport, "expected adapter client import to be added")

	sections := cliFile.AllSections()
	require.Len(t, sections, 2)
	require.Equal(t, "cli-dojsonrpc", sections[1].SectionName())
	require.Empty(t, cliFile.Section("cli-http-start"))
	require.Empty(t, cliFile.Section("cli-http-end"))
	require.IsType(t, codegen.NewJenniferSection("expected", func(*jen.Statement) {}), sections[1])
}

func TestGenerateExampleAdapterStubs_ReplacesStub(t *testing.T) {
	// Arrange: existing stub file with a header and a dummy body
	svc := &expr.ServiceExpr{Name: "Orchestrator"}
	header := &codegen.SectionTemplate{
		Name: headerSection,
		Data: map[string]any{
			"Imports": []*codegen.ImportSpec{
				{Path: "example.com/assistant/gen/mcp_orchestrator", Name: "mcporchestrator"},
			},
		},
	}
	body := &codegen.SectionTemplate{
		Name:   "body",
		Source: "func NewMcpOrchestrator() mcporchestrator.Service { return &mcpOrchestratorsrvc{} }",
	}
	stub := &codegen.File{Path: "mcp_orchestrator.go", Sections: []codegen.Section{header, body}}

	files, err := generateExampleAdapterStubs("example.com/assistant/gen", []*expr.ServiceExpr{svc}, []*codegen.File{stub})
	require.NoError(t, err)
	require.Len(t, files, 1)
	sections := files[0].AllSections()
	require.Len(t, sections, 2)
	require.Equal(t, exampleMCPStubSection, sections[1].SectionName())
	require.Contains(t, renderExampleSection(t, sections[1]), "NewMCPAdapter(NewOrchestrator()")
}

func TestGenerateExampleAdapterStubs_DynamicOnlyPromptUsesProviderConstructor(t *testing.T) {
	previousRoot := mcpexpr.Root
	mcpexpr.Root = mcpexpr.NewRoot()
	t.Cleanup(func() {
		mcpexpr.Root = previousRoot
	})

	svc := &expr.ServiceExpr{Name: "Orchestrator"}
	mcp := &mcpexpr.MCPExpr{Name: "orchestrator", Version: "1.0.0"}
	mcpexpr.Root.RegisterMCP(svc, mcp)
	mcpexpr.Root.RegisterDynamicPrompt(svc, &mcpexpr.DynamicPromptExpr{Name: "dynamic"})
	mcp.Finalize()

	header := &codegen.SectionTemplate{
		Name: headerSection,
		Data: map[string]any{
			"Imports": []*codegen.ImportSpec{
				{Path: "example.com/assistant/gen/mcp_orchestrator", Name: "mcporchestrator"},
			},
		},
	}
	stub := &codegen.File{Path: "mcp_orchestrator.go", Sections: []codegen.Section{
		header,
		&codegen.SectionTemplate{Name: "body", Source: "func placeholder() {}"},
	}}

	files, err := generateExampleAdapterStubs("example.com/assistant/gen", []*expr.ServiceExpr{svc}, []*codegen.File{stub})
	require.NoError(t, err)
	require.Len(t, files, 1)

	section := files[0].Section(exampleMCPStubSection)
	require.Len(t, section, 1)
	source := renderExampleSection(t, section[0])
	require.Contains(t, source, "NewMCPAdapter(NewOrchestrator(), nil, nil)")
}

func TestPatchCLIForServer_FailsOnUpstreamDrift(t *testing.T) {
	header := func() codegen.Section {
		return &codegen.SectionTemplate{
			Name: headerSection,
			Data: map[string]any{
				"Imports": []*codegen.ImportSpec{{Path: "example.com/assistant/gen/jsonrpc/cli/orchestrator/cli"}},
			},
		}
	}
	section := func(name string) codegen.Section {
		return &codegen.SectionTemplate{Name: name, Source: "// source"}
	}
	validFile := func() *codegen.File {
		return &codegen.File{
			Path: "cmd/orchestrator-cli/jsonrpc.go",
			Sections: []codegen.Section{
				header(),
				section("cli-http-start"),
				section("cli-http-end"),
			},
		}
	}

	svc := &expr.ServiceExpr{Name: "orchestrator", Methods: []*expr.MethodExpr{{Name: "EventsStream"}}}
	svr := &expr.ServerExpr{Name: "srv", Services: []string{"orchestrator"}}
	tests := []struct {
		name  string
		files func() []*codegen.File
		want  string
	}{
		{
			name: "missing expected path",
			files: func() []*codegen.File {
				file := validFile()
				file.Path = "cmd/orchestrator-cli/http.go"
				return []*codegen.File{file}
			},
			want: `expected one "cmd/orchestrator-cli/jsonrpc.go" file, found 0`,
		},
		{
			name:  "duplicate expected path",
			files: func() []*codegen.File { return []*codegen.File{validFile(), validFile()} },
			want:  `expected one "cmd/orchestrator-cli/jsonrpc.go" file, found 2`,
		},
		{
			name: "missing start",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections([]codegen.Section{header(), section("cli-http-end")})
				return []*codegen.File{file}
			},
			want: `expected one "cli-http-start" section, found 0`,
		},
		{
			name: "duplicate start",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections(append(file.AllSections(), section("cli-http-start")))
				return []*codegen.File{file}
			},
			want: `expected one "cli-http-start" section, found 2`,
		},
		{
			name: "missing end",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections([]codegen.Section{header(), section("cli-http-start")})
				return []*codegen.File{file}
			},
			want: `expected one "cli-http-end" section, found 0`,
		},
		{
			name: "duplicate end",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections(append(file.AllSections(), section("cli-http-end")))
				return []*codegen.File{file}
			},
			want: `expected one "cli-http-end" section, found 2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patchCLIForServer("orchestrator", svr, []*expr.ServiceExpr{svc}, tt.files())
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestGenerateExampleAdapterStubs_FailsOnPathAndHeaderDrift(t *testing.T) {
	svc := &expr.ServiceExpr{Name: "Orchestrator"}
	header := func() codegen.Section {
		return &codegen.SectionTemplate{
			Name: headerSection,
			Data: map[string]any{
				"Imports": []*codegen.ImportSpec{{Path: "example.com/assistant/gen/mcp_orchestrator", Name: "mcporchestrator"}},
			},
		}
	}
	validFile := func() *codegen.File {
		return &codegen.File{
			Path: "mcp_orchestrator.go",
			Sections: []codegen.Section{
				header(),
				&codegen.SectionTemplate{Name: "body", Source: "func placeholder() {}"},
			},
		}
	}
	tests := []struct {
		name  string
		files func() []*codegen.File
		want  string
	}{
		{
			name: "missing expected path",
			files: func() []*codegen.File {
				file := validFile()
				file.Path = "orchestrator.go"
				return []*codegen.File{file}
			},
			want: `expected one "mcp_orchestrator.go" file, found 0`,
		},
		{
			name:  "duplicate expected path",
			files: func() []*codegen.File { return []*codegen.File{validFile(), validFile()} },
			want:  `expected one "mcp_orchestrator.go" file, found 2`,
		},
		{
			name: "missing header",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections([]codegen.Section{&codegen.SectionTemplate{Name: "body", Source: "func placeholder() {}"}})
				return []*codegen.File{file}
			},
			want: `expected one "source-header" section, found 0`,
		},
		{
			name: "duplicate header",
			files: func() []*codegen.File {
				file := validFile()
				file.SetSections(append(file.AllSections(), header()))
				return []*codegen.File{file}
			},
			want: `expected one "source-header" section, found 2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generateExampleAdapterStubs("example.com/assistant/gen", []*expr.ServiceExpr{svc}, tt.files())
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func renderExampleSection(t *testing.T, section codegen.Section) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, section.Write(&buf))
	return buf.String()
}
