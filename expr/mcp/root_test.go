package mcp

import (
	"testing"

	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

type testExpression string

func TestRootExprWalkSetsUsesServiceNameOrder(t *testing.T) {
	root := &RootExpr{
		MCPServers: map[string]*MCPExpr{
			"beta":  testMCPServer("beta"),
			"gamma": testMCPServer("gamma"),
			"alpha": testMCPServer("alpha"),
		},
		DynamicPrompts: map[string][]*DynamicPromptExpr{
			"beta": {
				{Name: "beta-dynamic-prompt"},
			},
			"gamma": {
				{Name: "gamma-dynamic-prompt"},
			},
			"alpha": {
				{Name: "alpha-dynamic-prompt"},
			},
		},
		pendingStaticPrompts: map[string][]*PromptExpr{
			"aardvark": {
				{
					Name: "aardvark-prompt",
					Messages: []*MessageExpr{
						{Expression: testExpression("aardvark-message")},
					},
				},
			},
		},
	}

	var sets [][]string
	root.WalkSets(func(set eval.ExpressionSet) {
		sets = append(sets, expressionSetLabels(set))
	})

	require.Equal(t, [][]string{
		{"alpha", "beta", "gamma"},
		{"alpha-capabilities", "beta-capabilities", "gamma-capabilities"},
		{"alpha-tool", "beta-tool", "gamma-tool"},
		{"alpha-resource", "beta-resource", "gamma-resource"},
		{"aardvark-prompt", "alpha-prompt", "beta-prompt", "gamma-prompt"},
		{"aardvark-message", "alpha-message", "beta-message", "gamma-message"},
		{"alpha-dynamic-prompt", "beta-dynamic-prompt", "gamma-dynamic-prompt"},
	}, sets)
}

func TestRootExprValidateRejectsDuplicateMCPDeclarations(t *testing.T) {
	root := NewRoot()
	svc := &expr.ServiceExpr{Name: "assistant"}
	first := &MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		OAuth: &OAuthExpr{
			AuthorizationServers: []string{"https://auth.example.com"},
		},
	}
	second := &MCPExpr{
		Name:    "assistant-mcp-v2",
		Version: "2.0.0",
	}

	root.RegisterMCP(svc, first)
	root.RegisterMCP(svc, second)

	require.Same(t, first, root.GetMCP(svc))
	require.Equal(t, "assistant", first.Service.Name)
	err := root.Validate()
	require.ErrorContains(t, err, `duplicate MCP declaration for service "assistant"`)
}

func TestRootExprValidateRejectsStaticAndDynamicPromptNameCollision(t *testing.T) {
	root := NewRoot()
	svc := &expr.ServiceExpr{Name: "assistant"}
	root.RegisterMCP(svc, &MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Prompts: []*PromptExpr{
			{
				Name: "summarize",
				Messages: []*MessageExpr{
					{Role: "user", Content: "Summarize"},
				},
			},
		},
	})
	root.RegisterDynamicPrompt(svc, &DynamicPromptExpr{
		Name:   "summarize",
		Method: &expr.MethodExpr{Name: "build_summary_prompt"},
	})

	err := root.Validate()

	require.ErrorContains(t, err, `dynamic prompt name "summarize" for service "assistant" duplicates MCP prompt summarize`)
}

func TestRootExprValidateRejectsDuplicateDynamicPromptNames(t *testing.T) {
	root := NewRoot()
	svc := &expr.ServiceExpr{Name: "assistant"}
	root.RegisterDynamicPrompt(svc, &DynamicPromptExpr{
		Name:   "summarize",
		Method: &expr.MethodExpr{Name: "build_summary_prompt"},
	})
	root.RegisterDynamicPrompt(svc, &DynamicPromptExpr{
		Name:   "summarize",
		Method: &expr.MethodExpr{Name: "build_other_summary_prompt"},
	})

	err := root.Validate()

	require.ErrorContains(t, err, `dynamic prompt name "summarize" for service "assistant" duplicates MCP dynamic prompt summarize`)
}

func TestRootExprConsumeDeferredSkillDirectory(t *testing.T) {
	root := NewRoot()
	svc := &expr.ServiceExpr{Name: "assistant"}
	first := &SkillDirectoryExpr{Root: "first"}
	middle := &SkillDirectoryExpr{Root: "middle"}
	last := &SkillDirectoryExpr{Root: "last"}
	missing := &SkillDirectoryExpr{Root: "missing"}
	root.DeferSkillDirectory(svc, first)
	root.DeferSkillDirectory(svc, middle)
	root.DeferSkillDirectory(svc, last)

	require.False(t, root.ConsumeDeferredSkillDirectory(svc, missing))
	require.True(t, root.ConsumeDeferredSkillDirectory(svc, middle))
	require.Equal(t, []*SkillDirectoryExpr{first, last}, root.pendingSkillDirs[svc.Name])
	require.True(t, root.ConsumeDeferredSkillDirectory(svc, first))
	require.True(t, root.ConsumeDeferredSkillDirectory(svc, last))
	require.NotContains(t, root.pendingSkillDirs, svc.Name)
}

func testMCPServer(service string) *MCPExpr {
	return &MCPExpr{
		Name:    service + "-mcp",
		Service: &expr.ServiceExpr{Name: service},
		Capabilities: &CapabilitiesExpr{
			Expression: testExpression(service + "-capabilities"),
		},
		Tools: []*ToolExpr{
			{Name: service + "-tool"},
		},
		Resources: []*ResourceExpr{
			{Name: service + "-resource"},
		},
		Prompts: []*PromptExpr{
			{
				Name: service + "-prompt",
				Messages: []*MessageExpr{
					{
						Expression: testExpression(service + "-message"),
					},
				},
			},
		},
	}
}

func expressionSetLabels(set eval.ExpressionSet) []string {
	labels := make([]string, 0, len(set))
	for _, expression := range set {
		labels = append(labels, expressionLabel(expression))
	}
	return labels
}

func expressionLabel(expression eval.Expression) string {
	switch actual := expression.(type) {
	case *MCPExpr:
		return actual.Service.Name
	case *CapabilitiesExpr:
		return actual.Expression.EvalName()
	case *ToolExpr:
		return actual.Name
	case *ResourceExpr:
		return actual.Name
	case *PromptExpr:
		return actual.Name
	case *MessageExpr:
		return actual.Expression.EvalName()
	case *DynamicPromptExpr:
		return actual.Name
	default:
		return expression.EvalName()
	}
}

func (e testExpression) EvalName() string {
	return string(e)
}
