package codegen

import (
	"bytes"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	gocodegen "github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPExecutorFiles_DeduplicatesSameOriginToolsets(t *testing.T) {
	provider := &agentsExpr.ToolsetExpr{
		Name: "calc-remote",
		Provider: &agentsExpr.ProviderExpr{
			Kind:       agentsExpr.ProviderMCP,
			MCPService: "calc",
			MCPToolset: "core",
		},
	}
	used := &ToolsetData{
		Expr: &agentsExpr.ToolsetExpr{
			Name:     "calc-remote",
			Origin:   provider,
			Provider: provider.Provider,
		},
		QualifiedName:    "calc-remote",
		PathName:         "calc_remote",
		PackageName:      "calc_remote",
		Dir:              filepath.Join("gen", "alpha", "agents", "scribe", "calc_remote"),
		SpecsImportPath:  "example.com/gen/calc/toolsets/calc_remote",
		SpecsPackageName: "calc_remote_specs",
	}
	exported := &ToolsetData{
		Expr: &agentsExpr.ToolsetExpr{
			Name:     "calc-remote",
			Origin:   provider,
			Provider: provider.Provider,
		},
		QualifiedName:    used.QualifiedName,
		PathName:         used.PathName,
		PackageName:      used.PackageName,
		Dir:              used.Dir,
		SpecsImportPath:  used.SpecsImportPath,
		SpecsPackageName: used.SpecsPackageName,
	}
	agent := &AgentData{
		GoName:      "Scribe",
		AllToolsets: []*ToolsetData{used, exported},
	}

	files := mcpExecutorFiles(agent)

	require.Len(t, files, 1)
	require.Equal(t, filepath.Join(used.Dir, "mcp_executor.go"), files[0].Path)
}

func TestAgentSpecsAggregatorUniquesImportAliases(t *testing.T) {
	newToolset := func(name, pkg, importPath string) *ToolsetData {
		return &ToolsetData{
			Name:             name,
			QualifiedName:    name,
			SpecsPackageName: pkg,
			SpecsImportPath:  importPath,
			Tools: []*ToolData{
				{Name: "ping", ConstName: "Ping", QualifiedName: name + ".ping"},
			},
		}
	}

	cases := []struct {
		name        string
		toolsets    []*ToolsetData
		wantAliases map[string]string
	}{
		{
			name: "toolset named policy avoids runtime policy import",
			toolsets: []*ToolsetData{
				newToolset("policy", "policy", "example.com/gen/alpha/toolsets/policy"),
			},
			wantAliases: map[string]string{
				"example.com/gen/alpha/toolsets/policy": "policyspecs",
			},
		},
		{
			name: "toolset named tools avoids runtime tools import",
			toolsets: []*ToolsetData{
				newToolset("tools", "tools", "example.com/gen/alpha/toolsets/tools"),
			},
			wantAliases: map[string]string{
				"example.com/gen/alpha/toolsets/tools": "toolsspecs",
			},
		},
		{
			name: "identical slugs under different owners stay distinct",
			toolsets: []*ToolsetData{
				newToolset("shared-tools", "shared_tools", "example.com/gen/one/toolsets/shared_tools"),
				newToolset("shared.tools", "shared_tools", "example.com/gen/two/toolsets/shared_tools"),
			},
			wantAliases: map[string]string{
				"example.com/gen/one/toolsets/shared_tools": "shared_tools",
				"example.com/gen/two/toolsets/shared_tools": "shared_toolsspecs",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &AgentData{
				StructName:  "HelperAgent",
				Dir:         filepath.Join("gen", "alpha", "agents", "helper"),
				AllToolsets: tc.toolsets,
			}

			file := agentSpecsAggregatorFile(agent)
			require.NotNil(t, file)

			var buf bytes.Buffer
			for _, s := range file.AllSections() {
				require.NoError(t, s.Write(&buf))
			}
			aliases := importAliasesByPath(t, buf.String())
			for importPath, want := range tc.wantAliases {
				assert.Equalf(t, want, aliases[importPath], "alias for %s", importPath)
			}
			assertDistinctValues(t, aliases)
		})
	}
}

func TestAgentGeneratedSurfacesUseCachedUniqueToolNames(t *testing.T) {
	rawTools := []*ToolData{
		{Name: "get_x", QualifiedName: "helpers.get_x", ConstName: "GetX"},
		{Name: "getX", QualifiedName: "helpers.getX", ConstName: "GetX"},
	}
	entries := []*toolEntry{
		{
			Name:      "helpers.get_x",
			GoName:    "GetX",
			ConstName: "GetX",
			Payload:   &typeData{TypeName: "GetXPayload", ExportedCodec: "GetXPayloadCodec"},
			Result:    &typeData{TypeName: "GetXResult", ExportedCodec: "GetXResultCodec"},
		},
		{
			Name:      "helpers.getX",
			GoName:    "GetX",
			ConstName: "GetX2",
			Payload:   &typeData{TypeName: "GetX2Payload", ExportedCodec: "GetX2PayloadCodec"},
			Result:    &typeData{TypeName: "GetX2Result", ExportedCodec: "GetX2ResultCodec"},
		},
	}
	toolset := &ToolsetData{
		Name:             "helpers",
		QualifiedName:    "helpers",
		SpecsPackageName: "helpers",
		SpecsImportPath:  "example.com/gen/alpha/toolsets/helpers",
		Tools:            rawTools,
	}
	agent := &AgentData{
		Name:        "assistant",
		StructName:  "Assistant",
		Dir:         filepath.Join("gen", "alpha", "agents", "assistant"),
		AllToolsets: []*ToolsetData{toolset},
	}
	cache := newToolSpecsDataCache()
	cache.build = func(string, *service.Data, []*ToolData) (*toolSpecsData, error) {
		return &toolSpecsData{tools: entries}, nil
	}

	file, err := resolvedAgentSpecsAggregatorFile(agent, cache)
	require.NoError(t, err)
	require.NotNil(t, file)
	var aggregate bytes.Buffer
	for _, section := range file.AllSections() {
		require.NoError(t, section.Write(&aggregate))
	}
	require.Contains(t, aggregate.String(), "helpers.SpecGetX")
	require.Contains(t, aggregate.String(), "helpers.SpecGetX2")
	require.Contains(t, aggregate.String(), "helpers.GetX2")

	section := gocodegen.NewJenniferSection("collision-safe-agent-tools", func(stmt *jen.Statement) {
		data := agentToolsetFileData{Toolset: toolset, Tools: entries}
		emitAgentToolsAliases(stmt, data)
		emitAgentToolCallBuilders(stmt, data)
	})
	var helpers bytes.Buffer
	require.NoError(t, section.Write(&helpers))
	require.Contains(t, helpers.String(), "type GetX2Payload = helpersspecs.GetX2Payload")
	require.Contains(t, helpers.String(), "func NewGetX2Call(")
	require.Contains(t, helpers.String(), "GetX2PayloadCodec.ToJSON(args)")
}

func TestAgentRegistryUniquesAliasesAgainstLateFixedImports(t *testing.T) {
	tests := []struct {
		name        string
		agent       *AgentData
		toolsetPath string
		wantAlias   string
		fixedPath   string
		fixedAlias  string
	}{
		{
			name: "specs toolset avoids aggregate specs import",
			agent: &AgentData{
				StructName:          "HelperAgent",
				PackageName:         "helper",
				Dir:                 filepath.Join("gen", "alpha", "agents", "helper"),
				ToolSpecsImportPath: "example.com/gen/alpha/agents/helper/specs",
				ToolSpecsPackage:    "specs",
				Tools:               []*ToolData{{Name: "local"}},
				UsedToolsets: []*ToolsetData{{
					QualifiedName:    "specs",
					SpecsPackageName: "specs",
					SpecsImportPath:  "example.com/gen/alpha/toolsets/specs",
				}},
			},
			toolsetPath: "example.com/gen/alpha/toolsets/specs",
			wantAlias:   "specs2",
			fixedPath:   "example.com/gen/alpha/agents/helper/specs",
			fixedAlias:  "specs",
		},
		{
			name: "time toolset avoids standard library import",
			agent: &AgentData{
				StructName:  "HelperAgent",
				PackageName: "helper",
				Dir:         filepath.Join("gen", "alpha", "agents", "helper"),
				RunPolicy:   RunPolicyData{TimeBudget: time.Second},
				UsedToolsets: []*ToolsetData{{
					QualifiedName:    "time",
					SpecsPackageName: "time",
					SpecsImportPath:  "example.com/gen/alpha/toolsets/time",
				}},
			},
			toolsetPath: "example.com/gen/alpha/toolsets/time",
			wantAlias:   "time2",
			fixedPath:   "time",
			fixedAlias:  "time",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := agentRegistryFile(test.agent)
			require.NotNil(t, file)
			var buf bytes.Buffer
			require.NoError(t, file.AllSections()[0].Write(&buf))
			aliases := importAliasesByPath(t, buf.String())
			assert.Equal(t, test.wantAlias, aliases[test.toolsetPath])
			assert.Equal(t, test.fixedAlias, aliases[test.fixedPath])
		})
	}
}

// importAliasesByPath parses source and returns the effective package
// identifier bound by each import path.
func importAliasesByPath(t *testing.T, src string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "specs.go", src, parser.ImportsOnly)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Imports)
	out := make(map[string]string, len(parsed.Imports))
	for _, imp := range parsed.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		name := path.Base(importPath)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[importPath] = name
	}
	return out
}

// assertDistinctValues requires that no two imports bind the same identifier.
func assertDistinctValues(t *testing.T, aliases map[string]string) {
	t.Helper()
	seen := make(map[string]string, len(aliases))
	for importPath, alias := range aliases {
		prev, dup := seen[alias]
		require.Falsef(t, dup, "import identifier %q bound by both %q and %q", alias, prev, importPath)
		seen[alias] = importPath
	}
}

func TestToolSpecsDataCacheMemoizesByGenerateTuple(t *testing.T) {
	alpha := &service.Data{Name: "alpha"}
	beta := &service.Data{Name: "beta"}
	gammaName := "gamma"
	toolset := &ToolsetData{
		Name:              "docs",
		QualifiedName:     "alpha.docs",
		SourceService:     alpha,
		SourceServiceName: alpha.Name,
	}

	var builds int
	cache := newToolSpecsDataCache()
	cache.build = func(genpkg string, svc *service.Data, tools []*ToolData) (*toolSpecsData, error) {
		builds++
		return &toolSpecsData{genpkg: genpkg, svc: svc}, nil
	}

	first, err := cache.specsForToolset("example.com/gen", toolset)
	require.NoError(t, err)
	second, err := cache.specsForToolset("example.com/gen", toolset)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.Equal(t, 1, builds)

	otherGenpkg, err := cache.specsForToolset("example.com/other", toolset)
	require.NoError(t, err)
	require.NotSame(t, first, otherGenpkg)
	require.Equal(t, 2, builds)

	otherService := *toolset
	otherService.SourceService = beta
	otherService.SourceServiceName = beta.Name
	otherServiceSpecs, err := cache.specsForToolset("example.com/gen", &otherService)
	require.NoError(t, err)
	require.NotSame(t, first, otherServiceSpecs)
	require.Equal(t, 3, builds)

	namedService := *toolset
	namedService.SourceService = nil
	namedService.SourceServiceName = gammaName
	namedServiceSpecs, err := cache.specsForToolset("example.com/gen", &namedService)
	require.NoError(t, err)
	require.NotSame(t, first, namedServiceSpecs)
	require.Equal(t, 4, builds)

	otherToolset := *toolset
	otherToolset.QualifiedName = "alpha.notes"
	otherToolsetSpecs, err := cache.specsForToolset("example.com/gen", &otherToolset)
	require.NoError(t, err)
	require.NotSame(t, first, otherToolsetSpecs)
	require.Equal(t, 5, builds)
}

func TestToolSpecsDataCacheDistinguishesToolsetSubsets(t *testing.T) {
	svc := &service.Data{Name: "alpha"}
	firstRef := &ToolsetData{
		Name:              "docs",
		QualifiedName:     "alpha.docs",
		SourceService:     svc,
		SourceServiceName: svc.Name,
		Tools: []*ToolData{
			{Name: "notify", QualifiedName: "docs.notify"},
		},
	}
	secondRef := &ToolsetData{
		Name:              firstRef.Name,
		QualifiedName:     firstRef.QualifiedName,
		SourceService:     svc,
		SourceServiceName: svc.Name,
		Tools: []*ToolData{
			{Name: "log", QualifiedName: "docs.log"},
		},
	}

	var builds int
	cache := newToolSpecsDataCache()
	cache.build = func(genpkg string, svc *service.Data, tools []*ToolData) (*toolSpecsData, error) {
		builds++
		return &toolSpecsData{tools: []*toolEntry{{Name: tools[0].QualifiedName}}}, nil
	}

	firstSpecs, err := cache.specsForToolset("example.com/gen", firstRef)
	require.NoError(t, err)
	secondSpecs, err := cache.specsForToolset("example.com/gen", secondRef)
	require.NoError(t, err)
	firstSpecsAgain, err := cache.specsForToolset("example.com/gen", firstRef)
	require.NoError(t, err)

	require.NotSame(t, firstSpecs, secondSpecs)
	require.Same(t, firstSpecs, firstSpecsAgain)
	require.Equal(t, 2, builds)
	require.Equal(t, "docs.notify", firstSpecs.tools[0].Name)
	require.Equal(t, "docs.log", secondSpecs.tools[0].Name)
}
