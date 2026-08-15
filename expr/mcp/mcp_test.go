package mcp

import (
	"testing"

	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestMCPExpr_EvalName(t *testing.T) {
	m := &MCPExpr{
		Name:    "test-server",
		Service: &expr.ServiceExpr{Name: "test-service"},
	}
	require.Equal(t, "MCP server for test-service", m.EvalName())
}

func TestMCPExpr_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mcp     *MCPExpr
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid MCP server",
			mcp: &MCPExpr{
				Name:    "test-server",
				Version: "1.0.0",
				Service: &expr.ServiceExpr{Name: "test-service"},
			},
			wantErr: false,
		},
		{
			name: "missing name",
			mcp: &MCPExpr{
				Version: "1.0.0",
				Service: &expr.ServiceExpr{Name: "test-service"},
			},
			wantErr: true,
			errMsg:  "MCP server name is required",
		},
		{
			name: "missing version",
			mcp: &MCPExpr{
				Name:    "test-server",
				Service: &expr.ServiceExpr{Name: "test-service"},
			},
			wantErr: true,
			errMsg:  "MCP server version is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mcp.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMCPExpr_ValidateRejectsDuplicateToolNames(t *testing.T) {
	mcp := validMCPExpr()
	mcp.Tools = []*ToolExpr{
		{Name: "lookup", Description: "Lookup one", Method: &expr.MethodExpr{Name: "lookup_one"}},
		{Name: "lookup", Description: "Lookup two", Method: &expr.MethodExpr{Name: "lookup_two"}},
	}

	err := mcp.Validate()

	require.ErrorContains(t, err, `tool name "lookup" duplicates`)
	require.ErrorContains(t, err, `method "lookup_one"`)
}

func TestMCPExpr_ValidateRejectsMultipleToolsOnOneMethod(t *testing.T) {
	method := &expr.MethodExpr{Name: "lookup"}
	mcp := validMCPExpr()
	mcp.Tools = []*ToolExpr{
		{Name: "lookup_fast", Description: "Lookup quickly", Method: method},
		{Name: "lookup_deep", Description: "Lookup deeply", Method: method},
	}

	err := mcp.Validate()

	require.ErrorContains(t, err, `method "lookup" declares multiple MCP tools that generate helper collisions`)
	require.ErrorContains(t, err, `"lookup_fast" and "lookup_deep"`)
}

func TestMCPExpr_ValidateRejectsDuplicatePromptNames(t *testing.T) {
	mcp := validMCPExpr()
	mcp.Prompts = []*PromptExpr{
		{Name: "summarize", Messages: []*MessageExpr{{Role: "user", Content: "Summarize this"}}},
		{Name: "summarize", Messages: []*MessageExpr{{Role: "user", Content: "Summarize that"}}},
	}

	err := mcp.Validate()

	require.ErrorContains(t, err, `prompt name "summarize" duplicates`)
}

func TestMCPExpr_ValidateRejectsDuplicateResourceNamesAndURIs(t *testing.T) {
	mcp := validMCPExpr()
	mcp.Resources = []*ResourceExpr{
		{Name: "document", URI: "doc://one", Method: &expr.MethodExpr{Name: "read_one"}},
		{Name: "document", URI: "doc://two", Method: &expr.MethodExpr{Name: "read_two"}},
		{Name: "archive", URI: "doc://one", Method: &expr.MethodExpr{Name: "read_archive"}},
	}

	err := mcp.Validate()

	require.ErrorContains(t, err, `resource name "document" duplicates`)
	require.ErrorContains(t, err, `method "read_one"`)
	require.ErrorContains(t, err, `resource URI "doc://one" duplicates`)
}

func TestOAuthExprValidateReportsDiagnostics(t *testing.T) {
	oauth := &OAuthExpr{
		Scopes: []*ScopeExpr{
			{Name: "read"},
			{Name: "read"},
			{},
		},
		BearerMethodsSupported: []string{"cookie"},
		ResourceIdentifier:     "https://api.example.com/resource#fragment",
	}

	err := oauth.Validate()

	require.Error(t, err)
	require.NotPanics(t, func() {
		_ = err.Error()
	})
	require.ErrorContains(t, err, "MCP OAuth configuration: OAuth requires at least one AuthorizationServer")
	require.ErrorContains(t, err, `OAuth scope "read" declared more than once`)
	require.ErrorContains(t, err, "OAuth scope name is required")
	require.ErrorContains(t, err, `OAuth BearerMethodsSupported must be header, body, or query; got "cookie"`)
	require.ErrorContains(t, err, "OAuth ResourceIdentifier invalid: must not contain a fragment")
}

func TestMCPExpr_Finalize(t *testing.T) {
	t.Run("sets default transport", func(t *testing.T) {
		m := &MCPExpr{
			Name:    "test-server",
			Version: "1.0.0",
			Service: &expr.ServiceExpr{Name: "test-service"},
		}
		m.Finalize()
		require.Equal(t, "jsonrpc", m.Transport)
		require.NotNil(t, m.Capabilities)
	})

	t.Run("preserves existing transport", func(t *testing.T) {
		m := &MCPExpr{
			Name:      "test-server",
			Version:   "1.0.0",
			Transport: "sse",
			Service:   &expr.ServiceExpr{Name: "test-service"},
		}
		m.Finalize()
		require.Equal(t, "sse", m.Transport)
	})

	t.Run("enables capabilities based on content", func(t *testing.T) {
		m := &MCPExpr{
			Name:    "test-server",
			Version: "1.0.0",
			Service: &expr.ServiceExpr{Name: "test-service"},
			Tools: []*ToolExpr{
				{Name: "tool1", Description: "A tool"},
			},
			Resources: []*ResourceExpr{
				{Name: "resource1", URI: "file:///test"},
			},
			Prompts: []*PromptExpr{
				{Name: "prompt1", Messages: []*MessageExpr{{Role: "user", Content: "Hello"}}},
			},
		}
		m.Finalize()
		require.True(t, m.Capabilities.EnableTools)
		require.True(t, m.Capabilities.EnableResources)
		require.True(t, m.Capabilities.EnablePrompts)
	})

	t.Run("enables prompts for dynamic-only service", func(t *testing.T) {
		previousRoot := Root
		Root = NewRoot()
		t.Cleanup(func() {
			Root = previousRoot
		})

		svc := &expr.ServiceExpr{Name: "test-service"}
		Root.DynamicPrompts[svc.Name] = []*DynamicPromptExpr{{Name: "dynamic-prompt"}}
		m := &MCPExpr{
			Name:    "test-server",
			Version: "1.0.0",
			Service: svc,
		}

		m.Finalize()

		require.True(t, m.Capabilities.EnablePrompts)
	})
}

func TestMCPExprFinalizeIsIdempotent(t *testing.T) {
	previousRoot := Root
	Root = NewRoot()
	t.Cleanup(func() {
		Root = previousRoot
	})

	service := &expr.ServiceExpr{Name: "test-service", Description: "Service description"}
	method := &expr.MethodExpr{Name: "Read", Service: service, Description: "Method description"}
	Root.DynamicPrompts[service.Name] = []*DynamicPromptExpr{{Name: "dynamic-prompt"}}
	m := &MCPExpr{
		Name:    "test-server",
		Version: "1.0.0",
		Service: service,
		Tools:   []*ToolExpr{{Name: "tool1", Description: "A tool"}},
		Resources: []*ResourceExpr{{
			Name:   "resource1",
			URI:    "file:///test",
			Method: method,
		}},
		SkillDirectories: []*SkillDirectoryExpr{{Root: ".agents/skills"}},
	}

	m.Finalize()
	capabilities := m.Capabilities
	m.Finalize()

	require.Same(t, capabilities, m.Capabilities)
	require.Equal(t, "jsonrpc", m.Transport)
	require.Equal(t, "Service description", m.Description)
	require.Equal(t, "Method description", m.Resources[0].Description)
	require.True(t, m.Capabilities.EnableTools)
	require.True(t, m.Capabilities.EnableResources)
	require.True(t, m.Capabilities.EnablePrompts)
	require.Len(t, m.Tools, 1)
	require.Len(t, m.Resources, 1)
	require.Len(t, m.SkillDirectories, 1)
}

func validMCPExpr() *MCPExpr {
	return &MCPExpr{
		Name:    "test-server",
		Version: "1.0.0",
		Service: &expr.ServiceExpr{Name: "test-service"},
	}
}

func TestToolExpr_Validate(t *testing.T) {
	tests := []struct {
		name    string
		tool    *ToolExpr
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid tool",
			tool: &ToolExpr{
				Name:        "test-tool",
				Description: "A test tool",
			},
			wantErr: false,
		},
		{
			name: "missing name",
			tool: &ToolExpr{
				Description: "A test tool",
			},
			wantErr: true,
			errMsg:  "tool name is required",
		},
		{
			name: "missing description",
			tool: &ToolExpr{
				Name: "test-tool",
			},
			wantErr: true,
			errMsg:  "tool description is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tool.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMCPToolDiscoveryMetadataOptions(t *testing.T) {
	tool := &ToolExpr{
		Name:              "search",
		Description:       "Search indexed content",
		Title:             "Search Content",
		DiscoveryCategory: "knowledge",
		DiscoveryTags:     []string{"search", "retrieval"},
		DiscoveryKeywords: []string{"lookup", "documents"},
	}

	require.Equal(t, "Search Content", tool.Title)
	require.Equal(t, "knowledge", tool.DiscoveryCategory)
	require.Equal(t, []string{"search", "retrieval"}, tool.DiscoveryTags)
	require.Equal(t, []string{"lookup", "documents"}, tool.DiscoveryKeywords)
	require.NoError(t, tool.Validate())
}

func TestMCPToolValidation(t *testing.T) {
	t.Run("rejects agent runtime exposure on method-level MCP tool", func(t *testing.T) {
		tool := &ToolExpr{
			Name:            "lookup",
			Description:     "Lookup",
			ExposedSurfaces: []string{"agent_runtime"},
		}

		require.ErrorContains(t, tool.Validate(), "Expose(AgentRuntime) is invalid")
	})

	t.Run("rejects MCPPlacement on method-level MCP tool", func(t *testing.T) {
		tool := &ToolExpr{
			Name:                "lookup",
			Description:         "Lookup",
			MCPPlacementService: "assistant",
			MCPPlacementServer:  "assistant-mcp",
		}

		require.ErrorContains(t, tool.Validate(), "MCPPlacement is invalid")
	})

	t.Run("allows explicit MCP-only surface bookkeeping", func(t *testing.T) {
		tool := &ToolExpr{
			Name:            "lookup",
			Description:     "Lookup",
			ExposedSurfaces: []string{"mcp"},
		}

		require.NoError(t, tool.Validate())
	})
}

func TestMCPToolSearchPolicyValidation(t *testing.T) {
	enabled := true
	nameWeight := 10
	search := &ToolSearchExpr{
		DefaultMaxResults: 5,
		MinScore:          100,
		ExactMatchMode:    ToolSearchExactMatchNarrow,
		FuzzyNameMatching: &enabled,
		BroadFallback:     &enabled,
		Weights: ToolSearchWeightsExpr{
			Name: &nameWeight,
		},
	}
	require.NoError(t, search.Validate())

	search.DefaultMaxResults = -1
	require.ErrorContains(t, search.Validate(), "DefaultMaxResults must be non-negative")
	search.DefaultMaxResults = 5

	search.MinScore = -1
	require.ErrorContains(t, search.Validate(), "MinScore must be non-negative")
	search.MinScore = 100

	search.ExactMatchMode = "surprising"
	require.ErrorContains(t, search.Validate(), "ExactMatchMode must be narrow, boost, or off")
	search.ExactMatchMode = ToolSearchExactMatchNarrow

	negative := -1
	search.Weights.Title = &negative
	require.ErrorContains(t, search.Validate(), "Title weight must be non-negative")
}

func TestResourceExpr_Validate(t *testing.T) {
	tests := []struct {
		name     string
		resource *ResourceExpr
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid resource",
			resource: &ResourceExpr{
				Name: "test-resource",
				URI:  "file:///test",
			},
			wantErr: false,
		},
		{
			name: "missing name",
			resource: &ResourceExpr{
				URI: "file:///test",
			},
			wantErr: true,
			errMsg:  "resource name is required",
		},
		{
			name: "missing URI",
			resource: &ResourceExpr{
				Name: "test-resource",
			},
			wantErr: true,
			errMsg:  "resource URI is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.resource.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPromptExpr_Validate(t *testing.T) {
	tests := []struct {
		name    string
		prompt  *PromptExpr
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid prompt",
			prompt: &PromptExpr{
				Name: "test-prompt",
				Messages: []*MessageExpr{
					{Role: "user", Content: "Hello"},
					{Role: "assistant", Content: "Hi"},
				},
			},
			wantErr: false,
		},
		{
			name: "system role",
			prompt: &PromptExpr{
				Name: "test-prompt",
				Messages: []*MessageExpr{
					{Role: "system", Content: "Hello"},
				},
			},
			wantErr: true,
			errMsg:  "prompt message role must be user or assistant",
		},
		{
			name: "empty role",
			prompt: &PromptExpr{
				Name: "test-prompt",
				Messages: []*MessageExpr{
					{Content: "Hello"},
				},
			},
			wantErr: true,
			errMsg:  "prompt message role must be user or assistant",
		},
		{
			name: "missing name",
			prompt: &PromptExpr{
				Messages: []*MessageExpr{
					{Role: "user", Content: "Hello"},
				},
			},
			wantErr: true,
			errMsg:  "prompt name is required",
		},
		{
			name: "missing messages",
			prompt: &PromptExpr{
				Name:     "test-prompt",
				Messages: []*MessageExpr{},
			},
			wantErr: true,
			errMsg:  "prompt must have at least one message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.prompt.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEvalNames(t *testing.T) {
	t.Run("CapabilitiesExpr", func(t *testing.T) {
		c := &CapabilitiesExpr{}
		require.Equal(t, "MCP capabilities", c.EvalName())
	})

	t.Run("ToolExpr", func(t *testing.T) {
		tool := &ToolExpr{Name: "my-tool"}
		require.Equal(t, "MCP tool my-tool", tool.EvalName())
	})

	t.Run("ResourceExpr", func(t *testing.T) {
		r := &ResourceExpr{Name: "my-resource"}
		require.Equal(t, "MCP resource my-resource", r.EvalName())
	})

	t.Run("PromptExpr", func(t *testing.T) {
		p := &PromptExpr{Name: "my-prompt"}
		require.Equal(t, "MCP prompt my-prompt", p.EvalName())
	})

	t.Run("MessageExpr", func(t *testing.T) {
		m := &MessageExpr{}
		require.Equal(t, "MCP message", m.EvalName())
	})

	t.Run("DynamicPromptExpr", func(t *testing.T) {
		d := &DynamicPromptExpr{Name: "my-dynamic-prompt"}
		require.Equal(t, "MCP dynamic prompt my-dynamic-prompt", d.EvalName())
	})
}
