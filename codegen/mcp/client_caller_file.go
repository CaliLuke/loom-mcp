package codegen

import (
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
)

func clientCallerFile(data *AdapterData, svcName string) *codegen.File {
	if data == nil || data.ClientCaller == nil {
		return nil
	}
	path := filepath.Join(codegen.Gendir, "jsonrpc", "mcp_"+svcName, "client", "caller.go")
	sections := []*codegen.SectionTemplate{
		{
			Name:   "mcp-client-caller",
			Source: mcpTemplates.Read("mcp_client_caller"),
			Data:   data.ClientCaller,
		},
	}
	return &codegen.File{Path: path, SectionTemplates: sections}
}
