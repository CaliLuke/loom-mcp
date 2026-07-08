package codegen

import (
	"path/filepath"
	"testing"

	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom/codegen/service"
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
