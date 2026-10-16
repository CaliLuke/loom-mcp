package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetadataExpressionsRejectInvalidNestedConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*MCPExpr)
		want      string
	}{
		{"oauth", func(m *MCPExpr) {
			m.OAuth = &OAuthExpr{AuthorizationServers: []string{"%"}}
		}, "AuthorizationServer"},
		{"tool blank field", func(m *MCPExpr) {
			m.Tools = []*ToolExpr{{Name: "tool", Description: "Tool", DiscoveryCallTemplateArgs: map[string]any{" ": true}}}
		}, "field name must be non-empty"},
		{"tool invalid value", func(m *MCPExpr) {
			m.Tools = []*ToolExpr{{Name: "tool", Description: "Tool", DiscoveryCallTemplateArgs: map[string]any{"x": make(chan int)}}}
		}, "JSON-marshalable"},
		{"tool icon", func(m *MCPExpr) {
			m.Tools = []*ToolExpr{{Name: "tool", Description: "Tool", Icons: []*IconExpr{{Source: "icon.png", Theme: "future"}}}}
		}, "icon theme"},
		{"resource icon", func(m *MCPExpr) {
			m.Resources = []*ResourceExpr{{Name: "resource", URI: "resource://one", Icons: []*IconExpr{{}}}}
		}, "icon source"},
		{"prompt icon", func(m *MCPExpr) {
			m.Prompts = []*PromptExpr{{Name: "prompt", Messages: []*MessageExpr{{Role: "user", Content: "hello"}}, Icons: []*IconExpr{{}}}}
		}, "icon source"},
		{"runtime prompt identity", func(m *MCPExpr) {
			m.Prompts = []*PromptExpr{{Name: "prompt", Messages: []*MessageExpr{{Role: "user", Content: "hello"}}, Runtime: &RuntimePromptExpr{Role: "future"}}}
		}, "runtime prompt agent id"},
		{"runtime prompt arity", func(m *MCPExpr) {
			m.Prompts = []*PromptExpr{{Name: "prompt", Messages: []*MessageExpr{{Role: "user", Content: "one"}, {Role: "user", Content: "two"}}, Runtime: &RuntimePromptExpr{AgentID: "agent", Role: RuntimePromptRoleUser}}}
		}, "exactly one message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMCPExpr()
			tt.configure(m)
			var err error
			switch {
			case len(m.Tools) > 0:
				err = m.Tools[0].Validate()
			case len(m.Resources) > 0:
				err = m.Resources[0].Validate()
			case len(m.Prompts) > 0:
				err = m.Prompts[0].Validate()
			default:
				err = m.Validate()
			}
			require.ErrorContains(t, err, tt.want)
		})
	}
}
