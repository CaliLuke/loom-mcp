package tests

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

// Validates the Quickstart README via a golden for the stable header section
// and a few structural markers for the rest to avoid brittleness.
func TestQuickstart_Renders_Minimal(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ToolSpecsMinimal())

	content := fileContent(t, files, "AGENTS_QUICKSTART.md")
	require.NotEmpty(t, content)
	require.NotContains(t, content, "* **`calc.helpers`** ")

	// Compare the header + services overview against a golden with normalization.
	// Split at the start of section 2 to keep the golden focused and stable.
	split := "\n## 2. 🚀 The 3-Step Liftoff"
	var header string
	if idx := strings.Index(content, split); idx > 0 {
		header = content[:idx+1] // include trailing newline before the section header
	} else {
		t.Fatalf("expected quickstart section header %q", split)
	}
	testutil.AssertString(t, "testdata/golden/quickstart/minimal.header.md.golden", header)

	// Sanity markers beyond the header to ensure key content is present.
	require.Contains(t, content, "calc.scribe")
	require.Contains(t, content, "calc.helpers")
	require.Contains(t, content, "client := scribe.NewClient(rt)")
	require.Contains(t, content, "runtime.New(")
	require.Contains(t, content, "runtime.WithEngine(myTemporalEngine)")
	require.Contains(t, content, "temporal.NewWorker(temporal.Options{")
	require.Contains(t, content, "temporal.NewClient(...)")
	require.NotContains(t, content, "runtime.New(runtime.Options{")
	require.NotContains(t, content, "temporal.New(temporal.Options{")
	require.Contains(t, content, "[]*model.Message{")
	require.Contains(t, content, "## 4. 🧠 The Planner:")
	require.Contains(t, content, "gen/<service>/toolsets/<toolset>/")
	require.Contains(t, content, "runtime.ToolCallExecutorFunc(<toolsetpkg>.Execute)")
	require.Contains(t, content, "canonical IDs have the form `<toolset>.<tool>`")
	require.Contains(t, content, "case specs.<Tool>:")
	require.Contains(t, content, "specs.Init<Tool>MethodPayload(typedArgs)")
	require.Contains(t, content, "specs.Init<Tool>ToolResult(methodResult)")
	require.Contains(t, content, "&specs.<ToolResult>{")
	require.NotContains(t, content, "gen/<svc>/agents/<agent>/specs/<toolset>/")
	require.NotContains(t, content, "<svc>.<toolset>.<tool>")
	require.NotContains(t, content, "ToMethodPayload_<Tool>")
	require.NotContains(t, content, "ToToolReturn_<Tool>")
	require.NotContains(t, content, "<ToolReturn>")
	require.NotContains(t, content, "Use(MCPToolset(...))")
	require.NotContains(t, content, "Use(Toolset(FromMCP(...)))")
	require.NotContains(t, content, "mcpruntime.NewCaller(remoteClient)")
	require.Contains(t, content, "Toolset(FromMCP(...))")
	require.Contains(t, content, `<jsonrpc_client_pkg>.NewCaller(remoteClient, "<mcp-suite>")`)
	require.NotContains(t, content, "Service-Side Tool Providers (Registry-Routed Execution)")
}

func TestQuickstart_Disabled(t *testing.T) {
	design := func() {
		goadsl.API("calc", func() {
			DisableAgentDocs()
		})
		goadsl.Service("calc", func() {
			Agent("scribe", "Doc helper", func() {
				Use("helpers", func() {})
			})
		})
	}
	files := buildAndGenerate(t, design)

	// Ensure quickstart is not emitted
	for _, f := range files {
		require.NotEqual(t, "AGENTS_QUICKSTART.md", f.Path, "AGENTS_QUICKSTART.md should not be generated when DisableAgentDocs is set")
	}
}

func TestQuickstart_IncludesProvidersSection_WhenGenerated(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelf())

	content := fileContent(t, files, "AGENTS_QUICKSTART.md")
	require.NotEmpty(t, content)
	require.Contains(t, content, "Service-Side Tool Providers (Registry-Routed Execution)")
	require.Contains(t, content, "gen/<service>/toolsets/<toolset>/provider.go")
}
