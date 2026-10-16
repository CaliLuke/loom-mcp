package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootRejectsDuplicateDeclarationsAndUnresolvedAgentExports(t *testing.T) {
	for _, tt := range []struct {
		name string
		root *RootExpr
		want string
	}{
		{"registry", &RootExpr{Registries: []*RegistryExpr{{Name: "shared"}, {Name: "shared"}}}, "registry name \"shared\" duplicates"},
		{"toolset", &RootExpr{Toolsets: []*ToolsetExpr{{Name: "shared"}, {Name: "shared"}}}, "toolset name \"shared\" duplicates"},
		{"unresolved export", &RootExpr{Toolsets: []*ToolsetExpr{{Name: "lookup", AgentToolset: &AgentToolsetReferenceExpr{Service: "missing", Agent: "assistant", Toolset: "lookup"}}}}, "AgentToolset could not resolve"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.root.Prepare()
			require.ErrorContains(t, tt.root.Validate(), tt.want)
		})
	}
}

func TestToolValidationRejectsInvalidExposureDeclarations(t *testing.T) {
	for _, tt := range []struct {
		name     string
		surfaces []ToolSurface
		want     string
	}{
		{"blank", []ToolSurface{""}, "non-empty surfaces"},
		{"unknown", []ToolSurface{"future"}, "unknown surface"},
		{"missing binding", []ToolSurface{ToolSurfaceAgentRuntime, ToolSurfaceMCP}, "requires BindTo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolExpr{Name: "lookup", Description: "Lookup", Surfaces: tt.surfaces}
			require.ErrorContains(t, tool.Validate(), tt.want)
		})
	}
}
