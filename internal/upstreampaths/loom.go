// Package upstreampaths centralizes upstream module/import constants used by codegen and tests.
package upstreampaths

const (
	// LoomCoreModule is the current upstream module for core DSL/codegen/runtime APIs.
	LoomCoreModule = "github.com/CaliLuke/loom"

	// LoomCLIPackage is the go run target for the upstream generator CLI.
	LoomCLIPackage = LoomCoreModule + "/cmd/loom"

	// LoomPkgImportPath is the current upstream core package import path used in generated code.
	LoomPkgImportPath = LoomCoreModule + "/pkg"

	// LoomMCPModule is the current upstream module for MCP transport imports.
	// Keep this equal to LoomCoreModule until loom-mcp is published.
	LoomMCPModule = LoomCoreModule

	// LoomMCPHTTPImportPath is the generated import path for HTTP transport helpers.
	LoomMCPHTTPImportPath = LoomMCPModule + "/http"

	// LoomMCPJSONRPCImportPath is the generated import path for JSON-RPC transport helpers.
	LoomMCPJSONRPCImportPath = LoomMCPModule + "/jsonrpc"
)
