package agent

import (
	"testing"

	exprmcp "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestToolsetExpr_EvalName(t *testing.T) {
	ts := &ToolsetExpr{Name: "my-toolset"}
	require.Equal(t, `toolset "my-toolset"`, ts.EvalName())
}

func TestToolsetExprValidateRejectsUnknownOriginToolSelection(t *testing.T) {
	ts := &ToolsetExpr{
		Name:           "shared-tools",
		Origin:         &ToolsetExpr{Name: "shared-tools", Tools: []*ToolExpr{{Name: "search"}}},
		ToolSelections: []string{"missing"},
	}

	err := ts.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `selects unknown origin tool "missing" from toolset "shared-tools"`)
}

func TestToolsetExpr_Validate_ProviderMCP(t *testing.T) {
	preserveGlobalRoots(t)

	// Set up Goa root with a service for MCP provider validation
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.Root.Services = []*goaexpr.ServiceExpr{
		{Name: "existing-service"},
	}
	exprmcp.Root = exprmcp.NewRoot()
	exprmcp.Root.MCPServers["existing-service"] = &exprmcp.MCPExpr{
		Name: "mcp-server",
	}

	t.Run("valid MCP provider", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "mcp-tools",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "existing-service",
				MCPToolset: "mcp-server",
			},
		}
		err := ts.Validate()
		require.NoError(t, err)
	})

	t.Run("MCP provider missing toolset name", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "mcp-tools",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "existing-service",
				MCPToolset: "",
			},
		}
		err := ts.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "MCP server name is required")
	})

	t.Run("MCP provider with non-existent service", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "mcp-tools",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "non-existent-service",
				MCPToolset: "mcp-server",
			},
		}
		err := ts.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "FromMCP could not resolve service")
	})

	t.Run("MCP provider with non-existent server", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "mcp-tools",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "existing-service",
				MCPToolset: "missing-server",
			},
		}
		err := ts.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), `FromMCP could not resolve service "existing-service" MCP server "missing-server"`)
	})

	t.Run("external MCP provider with inline schemas", func(t *testing.T) {
		goaexpr.Root.Services = append(goaexpr.Root.Services, &goaexpr.ServiceExpr{Name: "external-service"})
		ts := &ToolsetExpr{
			Name: "external-tools",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "external-service",
				MCPToolset: "remote",
			},
			Tools: []*ToolExpr{{Name: "search"}},
		}
		err := ts.Validate()
		require.NoError(t, err)
	})
}

func TestToolsetExpr_Validate_ProviderRegistry(t *testing.T) {
	t.Run("valid registry provider", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "registry-tools",
			Provider: &ProviderExpr{
				Kind:        ProviderRegistry,
				Registry:    &RegistryExpr{Name: "corp-registry"},
				ToolsetName: "data-tools",
			},
		}
		err := ts.Validate()
		require.NoError(t, err)
	})

	t.Run("registry provider missing registry", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "registry-tools",
			Provider: &ProviderExpr{
				Kind:        ProviderRegistry,
				Registry:    nil,
				ToolsetName: "data-tools",
			},
		}
		err := ts.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "registry is required for FromRegistry provider")
	})

	t.Run("registry provider missing toolset name", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "registry-tools",
			Provider: &ProviderExpr{
				Kind:        ProviderRegistry,
				Registry:    &RegistryExpr{Name: "corp-registry"},
				ToolsetName: "",
			},
		}
		err := ts.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "toolset name is required for FromRegistry provider")
	})
}

func TestToolsetExpr_Validate_ProviderLocal(t *testing.T) {
	t.Run("local provider with nil Provider", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name:     "local-tools",
			Provider: nil,
		}
		err := ts.Validate()
		require.NoError(t, err)
	})

	t.Run("local provider with explicit ProviderLocal kind", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "local-tools",
			Provider: &ProviderExpr{
				Kind: ProviderLocal,
			},
		}
		err := ts.Validate()
		require.NoError(t, err)
	})
}

func TestToolsetExprValidateVersionRequiresRegistryProvider(t *testing.T) {
	sharedMCPProvider := &ProviderExpr{
		Kind:       ProviderMCP,
		MCPService: "assistant",
		MCPToolset: "assistant-mcp",
	}

	cases := []struct {
		name     string
		toolset  *ToolsetExpr
		provider *ProviderExpr
	}{
		{
			name:    "nil provider remains local",
			toolset: &ToolsetExpr{Name: "local"},
		},
		{
			name: "explicit local provider",
			toolset: &ToolsetExpr{
				Name:     "local",
				Provider: &ProviderExpr{Kind: ProviderLocal},
			},
		},
		{
			name: "mcp provider",
			toolset: &ToolsetExpr{
				Name:     "mcp",
				Provider: sharedMCPProvider,
			},
			provider: sharedMCPProvider,
		},
		{
			name: "skills provider",
			toolset: &ToolsetExpr{
				Name: "skills",
				Provider: &ProviderExpr{
					Kind:       ProviderSkills,
					SkillRoots: []string{".agents/skills"},
				},
			},
		},
		{
			name: "artifacts provider",
			toolset: &ToolsetExpr{
				Name:     "artifacts",
				Provider: &ProviderExpr{Kind: ProviderArtifacts},
			},
		},
		{
			name: "memory provider",
			toolset: &ToolsetExpr{
				Name:     "memory",
				Provider: &ProviderExpr{Kind: ProviderMemory},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.toolset.SetVersion("1.2.3")

			require.ErrorContains(t, tc.toolset.Validate(), "Version is only valid for FromRegistry toolsets")
			if tc.provider != nil {
				require.Empty(t, tc.provider.Version)
			}
			if tc.toolset.Provider == nil {
				require.Nil(t, tc.toolset.Provider)
			} else {
				require.Empty(t, tc.toolset.Provider.Version)
			}
		})
	}
}

func TestToolsetExprSetVersionDoesNotMutateSharedNonRegistryProvider(t *testing.T) {
	sharedMCPProvider := &ProviderExpr{
		Kind:       ProviderMCP,
		MCPService: "assistant",
		MCPToolset: "assistant-mcp",
	}
	origin := &ToolsetExpr{
		Name:     "assistant-mcp",
		Provider: sharedMCPProvider,
	}
	reference := &ToolsetExpr{
		Name:     "assistant-mcp",
		Provider: sharedMCPProvider,
		Origin:   origin,
	}

	reference.SetVersion("1.2.3")

	require.Empty(t, sharedMCPProvider.Version)
	require.Empty(t, origin.Provider.Version)
	require.ErrorContains(t, reference.Validate(), "Version is only valid for FromRegistry toolsets")
}

func TestToolsetExprValidateRejectsVersionOnNonRegistryProviderField(t *testing.T) {
	ts := &ToolsetExpr{
		Name: "mcp",
		Provider: &ProviderExpr{
			Kind:       ProviderMCP,
			MCPService: "assistant",
			MCPToolset: "assistant-mcp",
			Version:    "1.2.3",
		},
	}

	require.ErrorContains(t, ts.Validate(), "Version is only valid for FromRegistry toolsets")
}

func TestToolsetExprValidateRejectsMixedBindToServices(t *testing.T) {
	search := &ToolExpr{Name: "search"}
	search.RecordBinding("catalog", "Search")
	charge := &ToolExpr{Name: "charge"}
	charge.RecordBinding("billing", "Charge")
	ts := &ToolsetExpr{
		Name:  "ops",
		Tools: []*ToolExpr{search, charge},
	}

	err := ts.Validate()

	require.ErrorContains(t, err, `toolset "ops" cannot mix BindTo target services`)
	require.ErrorContains(t, err, `tool "search" binds to service "catalog"`)
	require.ErrorContains(t, err, `tool "charge" binds to service "billing"`)
}

func TestToolsetExprValidateAllowsSingleBindToService(t *testing.T) {
	search := &ToolExpr{Name: "search"}
	search.RecordBinding("catalog", "Search")
	update := &ToolExpr{Name: "update"}
	update.RecordBinding("catalog", "Update")
	ts := &ToolsetExpr{
		Name:  "ops",
		Tools: []*ToolExpr{search, update},
	}

	require.NoError(t, ts.Validate())
}

func TestToolsetExprValidateSkillsProviderModes(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "skills",
			Provider: &ProviderExpr{
				Kind:         ProviderSkills,
				SkillRoots:   []string{".agents/skills"},
				SkillPreload: SkillPreloadOnStart,
				SkillReload:  SkillReloadPerCall,
			},
		}
		require.NoError(t, ts.Validate())
	})

	t.Run("invalid preload", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "skills",
			Provider: &ProviderExpr{
				Kind:         ProviderSkills,
				SkillRoots:   []string{".agents/skills"},
				SkillPreload: SkillPreloadMode("sometimes"),
			},
		}
		require.ErrorContains(t, ts.Validate(), "unknown skill preload mode")
	})

	t.Run("invalid reload", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "skills",
			Provider: &ProviderExpr{
				Kind:        ProviderSkills,
				SkillRoots:  []string{".agents/skills"},
				SkillReload: SkillReloadMode("often"),
			},
		}
		require.ErrorContains(t, ts.Validate(), "unknown skill reload mode")
	})
}

func TestToolsetExpr_ProviderResolution(t *testing.T) {
	t.Run("toolset without provider is local", func(t *testing.T) {
		ts := &ToolsetExpr{Name: "local"}
		// Provider is nil, which means local toolset with inline schemas
		require.Nil(t, ts.Provider)
	})

	t.Run("toolset with MCP provider", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "mcp-backed",
			Provider: &ProviderExpr{
				Kind:       ProviderMCP,
				MCPService: "svc",
				MCPToolset: "mcp-server",
			},
		}
		require.NotNil(t, ts.Provider)
		require.Equal(t, ProviderMCP, ts.Provider.Kind)
		require.Equal(t, "svc", ts.Provider.MCPService)
		require.Equal(t, "mcp-server", ts.Provider.MCPToolset)
	})

	t.Run("toolset with registry provider", func(t *testing.T) {
		reg := &RegistryExpr{Name: "corp-registry", URL: "https://registry.corp.internal"}
		ts := &ToolsetExpr{
			Name: "registry-backed",
			Provider: &ProviderExpr{
				Kind:        ProviderRegistry,
				Registry:    reg,
				ToolsetName: "enterprise-tools",
				Version:     "1.2.3",
			},
		}
		require.NotNil(t, ts.Provider)
		require.Equal(t, ProviderRegistry, ts.Provider.Kind)
		require.Equal(t, reg, ts.Provider.Registry)
		require.Equal(t, "enterprise-tools", ts.Provider.ToolsetName)
		require.Equal(t, "1.2.3", ts.Provider.Version)
	})
}

func TestToolsetExpr_WalkSets(t *testing.T) {
	tool1 := &ToolExpr{Name: "tool1"}
	tool2 := &ToolExpr{Name: "tool2"}
	ts := &ToolsetExpr{
		Name:  "test-toolset",
		Tools: []*ToolExpr{tool1, tool2},
	}

	var walked []eval.Expression
	ts.WalkSets(func(set eval.ExpressionSet) {
		for _, e := range set {
			walked = append(walked, e)
		}
	})

	require.Len(t, walked, 2)
	require.Equal(t, tool1, walked[0])
	require.Equal(t, tool2, walked[1])
}
