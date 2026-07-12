package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	agentexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
)

func TestToolsetDSLRejectsInvalidDefinitionsAndProviders(t *testing.T) {
	cases := []struct {
		name   string
		design func()
		want   string
	}{
		{
			name:   "unsupported definition argument",
			design: func() { Toolset(42) },
			want:   "cannot use 42 (type int) as type name, provider option, or func()",
		},
		{
			name:   "empty definition name",
			design: func() { Toolset("") },
			want:   "toolset name must be non-empty",
		},
		{
			name:   "MCP service",
			design: func() { Toolset(FromMCP("", "server")) },
			want:   "FromMCP requires non-empty service name",
		},
		{
			name:   "MCP toolset",
			design: func() { Toolset(FromMCP("svc", "")) },
			want:   "FromMCP requires non-empty toolset name",
		},
		{
			name:   "unsupported skills argument",
			design: func() { Toolset(FromSkills(".agents/skills", 42)) },
			want:   "FromSkills accepts skill roots and SkillProviderOption values, got int",
		},
		{
			name:   "empty skills roots",
			design: func() { Toolset(FromSkills(" ", "")) },
			want:   "FromSkills requires at least one non-empty skill root",
		},
		{
			name:   "nil registry",
			design: func() { Toolset(FromRegistry(nil, "tools")) },
			want:   "FromRegistry requires a non-nil registry",
		},
		{
			name: "empty registry toolset",
			design: func() {
				registry := Registry("corp", func() { URL("https://registry.example.com") })
				Toolset(FromRegistry(registry, ""))
			},
			want: "FromRegistry requires non-empty toolset name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, tc.design, tc.want)
		})
	}
}

func TestToolsetDSLRejectsInvalidReferences(t *testing.T) {
	var nilToolset *agentexpr.ToolsetExpr
	cases := []struct {
		name string
		use  func()
		want string
	}{
		{
			name: "nil expression",
			use:  func() { Use(nilToolset) },
			want: "toolset reference cannot be nil",
		},
		{
			name: "empty inline name",
			use:  func() { Use("") },
			want: "toolset name must be non-empty",
		},
		{
			name: "unsupported reference",
			use:  func() { Use(42) },
			want: "toolset must be referenced by name or Toolset expression",
		},
		{
			name: "empty agent toolset component",
			use:  func() { Use(AgentToolset("svc", "", "tools")) },
			want: "AgentToolset requires non-empty service, agent, and toolset",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, agentUseDesign(tc.use), tc.want)
		})
	}
}

func agentUseDesign(use func()) func() {
	return func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				use()
			})
		})
	}
}
