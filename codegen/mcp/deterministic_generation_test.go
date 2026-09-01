package codegen

import (
	"path/filepath"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestMCPGenerationIsDeterministicAcrossFreshDesigns(t *testing.T) {
	first := renderDeterministicGenerationFixture(t)
	second := renderDeterministicGenerationFixture(t)

	require.Equal(t, first, second)
	require.Contains(t, first, "gen/mcp_assistant/adapter_server.go")
	require.Contains(t, first, "gen/mcp_assistant/register.go")
	require.Contains(t, first, "gen/mcp_assistant/sdk_server.go")
}

func TestGenerationTimeJSONUsesDeterministicObjectOrdering(t *testing.T) {
	tool := &mcpexpr.ToolExpr{
		DiscoveryCategory: "knowledge",
		DiscoveryTags:     []string{"search", "retrieval"},
		DiscoveryKeywords: []string{"lookup", "documents"},
		DiscoveryCallTemplateArgs: map[string]any{
			"zeta":  map[string]any{"z": 2, "a": 1},
			"alpha": map[string]any{"enabled": true, "count": 3},
		},
	}
	requireExactJSON(t,
		`{"com.github.caliluke.loom-mcp/discovery":{"call_template_arguments":{"alpha":{"count":3,"enabled":true},"zeta":{"a":1,"z":2}},"category":"knowledge","keywords":["lookup","documents"],"tags":["search","retrieval"]}}`,
		mcpDiscoveryMetaJSON(tool),
	)

	meta := expr.MetaExpr{
		"mcp:annotation:readOnlyHint":    []string{"true"},
		"mcp:annotation:openWorldHint":   []string{"false"},
		"mcp:annotation:destructiveHint": []string{"false"},
	}
	requireExactJSON(t,
		`{"destructiveHint":false,"openWorldHint":false,"readOnlyHint":true}`,
		mcpAnnotationJSON(meta),
	)

	exampleAttr := &expr.AttributeExpr{
		Type: expr.Any,
		UserExamples: []*expr.ExampleExpr{{Value: map[string]any{
			"zeta":  map[string]any{"z": 2, "a": 1},
			"alpha": map[string]any{"enabled": true, "count": 3},
		}}},
	}
	requireExactJSON(t,
		`{"alpha":{"count":3,"enabled":true},"zeta":{"a":1,"z":2}}`,
		buildExampleJSON(exampleAttr),
	)

	canonicalAttr := &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "zeta", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "alpha", Attribute: &expr.AttributeExpr{Type: expr.Int}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"zeta", "alpha"}},
	}
	requireExactJSON(t, `{"alpha":0,"zeta":"example"}`, synthesizeCanonicalExample(canonicalAttr))

	union := &expr.Union{TypeName: "Action", Values: []*expr.NamedAttributeExpr{
		{
			Name: "list",
			Attribute: &expr.AttributeExpr{
				Type: &expr.Object{
					{Name: "zeta", Attribute: &expr.AttributeExpr{Type: expr.String}},
					{Name: "alpha", Attribute: &expr.AttributeExpr{Type: expr.Int}},
				},
				Validation: &expr.ValidationExpr{Required: []string{"zeta", "alpha"}},
				Meta:       expr.MetaExpr{"oneof:type:tag": []string{"list"}},
			},
		},
		{
			Name: "get",
			Attribute: &expr.AttributeExpr{
				Type:       &expr.Object{{Name: "beta", Attribute: &expr.AttributeExpr{Type: expr.Boolean}}},
				Validation: &expr.ValidationExpr{Required: []string{"beta"}},
				Meta:       expr.MetaExpr{"oneof:type:tag": []string{"get"}},
			},
		},
	}}
	unionPayload := &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "zeta", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "request", Attribute: &expr.AttributeExpr{Type: union}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"zeta", "request"}},
	}
	unionExamples := tagExamples(unionPayload, "request", union)
	requireExactJSON(t,
		`{"request":{"type":"list","value":{"alpha":0,"zeta":"example"}},"zeta":"example"}`,
		unionExamples["list"],
	)
	requireExactJSON(t,
		`{"request":{"type":"get","value":{"beta":false}},"zeta":"example"}`,
		unionExamples["get"],
	)
}

func requireExactJSON(t *testing.T, want string, got string) {
	t.Helper()
	if got != want {
		t.Fatalf("JSON bytes differ\nwant: %s\n got: %s", want, got)
	}
}

func renderDeterministicGenerationFixture(t *testing.T) map[string]string {
	t.Helper()

	previousRoot := mcpexpr.Root
	mcpexpr.Root = mcpexpr.NewRoot()
	defer func() {
		mcpexpr.Root = previousRoot
	}()

	svc, methods := testService("assistant", "search", "compose")
	methods["search"].Meta = expr.MetaExpr{
		"mcp:annotation:readOnlyHint":    []string{"true"},
		"mcp:annotation:openWorldHint":   []string{"false"},
		"mcp:annotation:destructiveHint": []string{"false"},
	}
	methods["search"].Payload = deterministicObjectPayload()
	methods["search"].Result = deterministicObjectPayload()
	methods["compose"].Payload = deterministicExamplePayload()
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name: "assistant", Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{{
			Name: "search", Description: "Search content", Method: methods["search"],
			DiscoveryCategory: "knowledge",
			DiscoveryTags:     []string{"search", "retrieval"},
			DiscoveryKeywords: []string{"lookup", "documents"},
			DiscoveryCallTemplateArgs: map[string]any{
				"zeta":  map[string]any{"z": 2, "a": 1},
				"alpha": map[string]any{"enabled": true, "count": 3},
			},
		}},
	})
	mcpexpr.Root.RegisterDynamicPrompt(svc, &mcpexpr.DynamicPromptExpr{
		Name: "compose", Description: "Compose an answer", Method: methods["compose"],
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	rendered := make(map[string]string, len(files))
	for _, file := range files {
		rendered[filepath.ToSlash(file.Path)] = renderGeneratedFile(t, file)
	}
	return rendered
}

func deterministicObjectPayload() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "zeta", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "settings", Attribute: &expr.AttributeExpr{Type: &expr.Map{
				KeyType:  &expr.AttributeExpr{Type: expr.String},
				ElemType: &expr.AttributeExpr{Type: expr.String},
			}}},
			{Name: "alpha", Attribute: &expr.AttributeExpr{Type: expr.Int}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"zeta", "settings", "alpha"}},
	}
}

func deterministicExamplePayload() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: expr.Any,
		UserExamples: []*expr.ExampleExpr{{Value: map[string]any{
			"zeta":  map[string]any{"z": 2, "a": 1},
			"alpha": map[string]any{"enabled": true, "count": 3},
		}}},
	}
}
