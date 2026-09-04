package codegen

import (
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom/codegen"
)

type localProviderData struct {
	Package            string
	ConstructorName    string
	ServiceName        string
	SuiteQualifiedName string
	Description        string
}

func localProviderFile(data *AdapterData) *codegen.File {
	if data == nil || data.Register == nil || len(data.Tools) == 0 {
		return nil
	}
	baseName := strings.TrimSuffix(data.Register.HelperName, "Toolset")
	templateData := localProviderData{
		Package:            data.MCPPackage,
		ConstructorName:    "New" + baseName + "LocalToolsetRegistration",
		ServiceName:        data.Register.ServiceName,
		SuiteQualifiedName: data.Register.SuiteQualifiedName,
		Description:        data.Register.Description,
	}
	path := filepath.Join(codegen.Gendir, "mcp_"+codegen.SnakeCase(data.ServiceName), "local_provider.go")
	imports := []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Name: "json", Path: "encoding/json/v2"},
		{Name: "jsontext", Path: "encoding/json/jsontext"},
		{Path: "errors"},
		{Path: "strings"},
		{Path: "github.com/CaliLuke/loom/pkg", Name: "loom"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "agentsruntime"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp", Name: "mcpruntime"},
	}
	return &codegen.File{
		Path: path,
		Sections: []codegen.Section{
			codegen.Header("MCP local progressive-discovery provider", templateData.Package, imports),
			codegen.NewRawSection("mcp-local-progressive-discovery-provider", mcpTemplates.MustRender("local_provider", templateData)),
		},
	}
}
