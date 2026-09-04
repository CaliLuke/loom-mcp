package codegen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalProviderFileGeneratesProgressiveDiscoveryRegistration(t *testing.T) {
	t.Parallel()

	data := &AdapterData{
		ServiceName: "catalog",
		MCPPackage:  "mcpcatalog",
		Tools:       []*ToolAdapter{{Name: "lookup"}},
		Register: &RegisterData{
			HelperName:         "CatalogCatalogMcpToolset",
			ServiceName:        "catalog",
			SuiteQualifiedName: "catalog.catalog-mcp",
			Description:        "Catalog tools",
		},
	}

	file := localProviderFile(data)
	require.NotNil(t, file)
	source := renderGeneratedFile(t, file)
	require.Contains(t, source, "func NewCatalogCatalogMcpLocalToolsetRegistration(adapter *MCPAdapter)")
	require.Contains(t, source, "adapter.toolSearchSyntheticTools()")
	require.Contains(t, source, "adapter.visibleToolCatalog(adapter.generatedToolCatalog())")
	require.Contains(t, source, "return a.handleSearchTools(ctx, payload, stream)")
	require.Contains(t, source, "return a.handleCallToolProxy(ctx, payload, stream)")
	require.Contains(t, source, "return a.executeRealTool(ctx, payload, stream)")
	require.Contains(t, source, "ctx = mcpruntime.WithProjectedToolCallMeta(ctx, agentsruntime.ToolCallMeta{")
	require.Contains(t, source, "RunID:            call.RunID")
	require.Contains(t, source, "ParentToolCallID: call.ParentToolCallID")
	require.Contains(t, source, "result.Result = loom.JSONValue(structuredContent)")
}

func TestLocalProviderFileRequiresGeneratedTools(t *testing.T) {
	t.Parallel()

	require.Nil(t, localProviderFile(&AdapterData{Register: &RegisterData{}}))
}
