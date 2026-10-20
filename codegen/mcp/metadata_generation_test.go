package codegen

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	loomgenerator "github.com/CaliLuke/loom/codegen/generator"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePreservesDiscoveryMetadataAndPromptArguments(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()
	svc, methods := testService("metadata", "lookup", "draft", "read")
	for _, method := range methods {
		method.Prepare()
		method.StreamingResult = nil
	}
	methods["draft"].Payload = &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: "DraftInput", AttributeExpr: &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "topic", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Subject to explain"}},
			{Name: "tone", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Writing tone", Validation: &expr.ValidationExpr{Values: []any{"brief", "detailed"}}}},
		}, Validation: &expr.ValidationExpr{Required: []string{"topic"}},
	}}}
	icons := []*mcpexpr.IconExpr{{Source: "https://example.com/icon.svg", MIMEType: "image/svg+xml", Sizes: []string{"48x48", "any"}, Theme: "dark"}, {Source: "https://example.com/plain.png"}}
	enabled, disabled := true, false
	name, title, metadata, description, parameters, fuzzy := 11, 22, 33, 44, 55, 66
	server := &mcpexpr.MCPExpr{Name: "metadata", Version: "1.0.0", Description: "Metadata service", WebsiteURL: "https://example.com/docs", Icons: icons,
		ToolSearch: &mcpexpr.ToolSearchExpr{DefaultMaxResults: 7, MinScore: 2, ExactMatchMode: "boost", FuzzyNameMatching: &disabled, BroadFallback: &enabled, Weights: mcpexpr.ToolSearchWeightsExpr{Name: &name, Title: &title, Metadata: &metadata, Description: &description, Parameters: &parameters, FuzzyName: &fuzzy}},
		Tools:      []*mcpexpr.ToolExpr{{Name: "lookup", Method: methods["lookup"], Icons: icons}},
		Resources:  []*mcpexpr.ResourceExpr{{Name: "record", URI: "record://current", Method: methods["read"], Icons: icons}},
		Prompts:    []*mcpexpr.PromptExpr{{Name: "welcome", Description: "Greeting", Icons: icons, Messages: []*mcpexpr.MessageExpr{{Role: "user", Content: "Welcome"}}}},
	}
	mcpexpr.Root.RegisterMCP(svc, server)
	mcpexpr.Root.DynamicPrompts[svc.Name] = []*mcpexpr.DynamicPromptExpr{{Name: "draft", Description: "Write a draft", Method: methods["draft"], Icons: icons}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	files, err := Generate("example.com/metadata/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_metadata/adapter_server.go"))
	sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_metadata/sdk_server.go"))
	compileMetadataModule(t, "example.com/metadata", root, files)
	for _, source := range []string{adapter, sdk} {
		_, err := parser.ParseFile(token.NewFileSet(), "generated.go", "package generated\n"+source, parser.AllErrors)
		require.NoError(t, err)
		assert.Contains(t, source, `"https://example.com/icon.svg"`)
		assert.Contains(t, source, `"image/svg+xml"`)
		assert.Contains(t, source, `"48x48"`)
		assert.Contains(t, source, `"dark"`)
	}
	assert.Contains(t, adapter, `Missing required argument: %s`)
	assert.Contains(t, adapter, `"topic"`)
	assert.Contains(t, sdk, `"Subject to explain"`)
	assert.Contains(t, sdk, `"brief"`)
	assert.Contains(t, sdk, `"detailed"`)
	assert.Contains(t, sdk, `"https://example.com/docs"`)
	for _, field := range []struct{ name, value string }{{"maxResults", "7"}, {"minScore", "2"}, {"exactMatchMode", `"boost"`}, {"fuzzyNameMatching", "false"}, {"broadFallback", "true"}, {"nameWeight", "11"}, {"titleWeight", "22"}, {"metadataWeight", "33"}, {"descriptionWeight", "44"}, {"parameterWeight", "55"}, {"fuzzyNameWeight", "66"}} {
		assert.Regexp(t, field.name+`:\s*`+field.value, adapter)
	}
}

func TestGenerateUnionRecoveryKeepsCallerSelectedVariant(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()
	svc, methods := testService("recovery", "lookup")
	methods["lookup"].Prepare()
	methods["lookup"].StreamingResult = nil
	union := &expr.Union{TypeName: "Action", TypeKey: "kind", ValueKey: "value", Values: []*expr.NamedAttributeExpr{
		{Name: "list", Attribute: &expr.AttributeExpr{Type: &expr.Object{{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, DefaultValue: 50}}}}},
		{Name: "history", Attribute: &expr.AttributeExpr{Type: &expr.Object{{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, DefaultValue: 10}}}}},
	}}
	// Match DSL appendUnionBranch: inline object branches are named before codegen.
	for _, branch := range union.Values {
		branch.Attribute.Type = expr.Root.NewUnionBranch("Action"+gcodegen.Goify(branch.Name, true), expr.DupAtt(branch.Attribute))
	}
	methods["lookup"].Payload = &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: "LookupPayload", AttributeExpr: &expr.AttributeExpr{Type: &expr.Object{{Name: "request", Attribute: &expr.AttributeExpr{Type: union}}}, Validation: &expr.ValidationExpr{Required: []string{"request"}}}}}
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{Name: "recovery", Version: "1.0.0", Tools: []*mcpexpr.ToolExpr{{Name: "lookup", Method: methods["lookup"]}}})
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	files, err := Generate("example.com/recovery/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	compileMetadataModule(t, "example.com/recovery", root, files)
	source := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_recovery/adapter_server.go"))
	_, err = parser.ParseFile(token.NewFileSet(), "adapter.go", "package generated\n"+source, parser.AllErrors)
	require.NoError(t, err)
	assert.Contains(t, source, "func lookupRecoveryExample(")
	assert.Contains(t, source, `top["request"]`)
	assert.Contains(t, source, `envelope["kind"]`)
	assert.Contains(t, source, `case "history":`)
	assert.Contains(t, source, `case "list":`)
	assert.Contains(t, source, `example := lookupRecoveryExample(raw)`)
	// Each recovery branch must retain its own authored default.
	selector := source[strings.Index(source, "func lookupRecoveryExample("):strings.Index(source, "func lookupInputRecovery(")]
	assert.Contains(t, selector, `\"limit\":10`)
	assert.Contains(t, selector, `\"limit\":50`)
}

// compileMetadataModule checks the full service and adapter package graph.
func compileMetadataModule(t *testing.T, module string, root *expr.RootExpr, files []*gcodegen.File) {
	t.Helper()
	serviceFiles, err := loomgenerator.Service(module+"/gen", []eval.Root{root})
	require.NoError(t, err)
	dir := t.TempDir()
	for _, file := range append(serviceFiles, files...) {
		_, err := file.Render(dir)
		require.NoError(t, err, file.Path)
	}
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	source, err := os.OpenRoot(repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, source.Close())
	})
	outputRoot, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, outputRoot.Close())
	})
	mod, err := source.ReadFile("go.mod")
	require.NoError(t, err)
	modText := strings.Replace(string(mod), "module github.com/CaliLuke/loom-mcp/v2", "module "+module, 1) + "\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\nreplace github.com/CaliLuke/loom-mcp/v2 => " + repo + "\n"
	require.NoError(t, outputRoot.WriteFile("go.mod", []byte(modText), 0600))
	sum, err := source.ReadFile("go.sum")
	require.NoError(t, err)
	require.NoError(t, outputRoot.WriteFile("go.sum", sum, 0600))
	command := exec.CommandContext(t.Context(), "go", "test", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "generated metadata module: %s", output)
}
