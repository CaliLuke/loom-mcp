package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

// TestAgentToolsetReferenceToExportedHandleMaterializesTools reproduces GitHub
// issue #115: a Use(AgentToolset(...)) reference pointing at an Export(handle)
// clone must materialize the defining toolset's tools regardless of service
// declaration order.
func TestAgentToolsetReferenceToExportedHandleMaterializesTools(t *testing.T) {
	cases := []struct {
		name          string
		providerFirst bool
	}{
		{name: "provider first", providerFirst: true},
		{name: "consumer first", providerFirst: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSL(t, func() {
				API("test", func() {})
				shared := Toolset("shared-tools", func() {
					Tool("t1", "tool one", func() {})
					Tool("t2", "tool two", func() {})
				})
				provider := func() {
					Service("producer-svc", func() {
						Agent("producer", "desc", func() {
							Export(shared)
						})
					})
				}
				consumer := func() {
					Service("consumer-svc", func() {
						Agent("consumer", "desc", func() {
							Use(AgentToolset("producer-svc", "producer", "shared-tools"))
						})
					})
				}
				if tc.providerFirst {
					provider()
					consumer()
					return
				}
				consumer()
				provider()
			})

			used := requireConsumerUsedToolset(t)
			require.NotNil(t, used.Origin, "AgentToolset reference should resolve to an origin")
			require.Len(t, used.Tools, 2)
			require.Equal(t, "t1", used.Tools[0].Name)
			require.Equal(t, "t2", used.Tools[1].Name)
		})
	}
}

// TestUseExportedToolsetHandleMaterializesTools verifies that consuming a
// toolset handle directly with Use materializes tools in both declaration
// orders, including when the consumed handle is itself an Export clone whose
// own tools resolve from a defining origin.
func TestUseExportedToolsetHandleMaterializesTools(t *testing.T) {
	cases := []struct {
		name          string
		providerFirst bool
	}{
		{name: "provider first", providerFirst: true},
		{name: "consumer first", providerFirst: false},
	}

	for _, tc := range cases {
		t.Run("defining handle "+tc.name, func(t *testing.T) {
			runDSL(t, func() {
				API("test", func() {})
				shared := Toolset("shared-tools", func() {
					Tool("t1", "tool one", func() {})
				})
				provider := func() {
					Service("producer-svc", func() {
						Agent("producer", "desc", func() {
							Export(shared)
						})
					})
				}
				consumer := func() {
					Service("consumer-svc", func() {
						Agent("consumer", "desc", func() {
							Use(shared)
						})
					})
				}
				if tc.providerFirst {
					provider()
					consumer()
					return
				}
				consumer()
				provider()
			})

			used := requireConsumerUsedToolset(t)
			require.NotNil(t, used.Origin)
			require.Len(t, used.Tools, 1)
			require.Equal(t, "t1", used.Tools[0].Name)
		})
	}

	for _, tc := range cases {
		t.Run("service export clone handle "+tc.name, func(t *testing.T) {
			var exported *agentsexpr.ToolsetExpr
			runDSL(t, func() {
				API("test", func() {})
				shared := Toolset("shared-tools", func() {
					Tool("t1", "tool one", func() {})
				})
				provider := func() {
					Service("producer-svc", func() {
						exported = Export(shared)
					})
				}
				consumer := func() {
					Service("consumer-svc", func() {
						Agent("consumer", "desc", func() {
							Use(exported)
						})
					})
				}
				if tc.providerFirst {
					provider()
					consumer()
					return
				}
				consumer()
				provider()
			})

			used := requireConsumerUsedToolset(t)
			require.Equal(t, exported, used.Origin)
			require.Len(t, used.Tools, 1)
			require.Equal(t, "t1", used.Tools[0].Name)
		})
	}
}

// TestAgentToolsetReferenceToolSubsetting verifies that Tool("name") overlays
// on an AgentToolset reference validate and materialize against the real
// origin tool list even when the origin is an Export clone resolved later.
func TestAgentToolsetReferenceToolSubsetting(t *testing.T) {
	design := func(selection string) func() {
		return func() {
			API("test", func() {})
			shared := Toolset("shared-tools", func() {
				Tool("t1", "tool one", func() {})
				Tool("t2", "tool two", func() {})
			})
			Service("consumer-svc", func() {
				Agent("consumer", "desc", func() {
					Use(AgentToolset("producer-svc", "producer", "shared-tools"), func() {
						Tool(selection)
					})
				})
			})
			Service("producer-svc", func() {
				Agent("producer", "desc", func() {
					Export(shared)
				})
			})
		}
	}

	t.Run("known origin tool", func(t *testing.T) {
		runDSL(t, design("t2"))

		used := requireConsumerUsedToolset(t)
		require.Len(t, used.Tools, 1)
		require.Equal(t, "t2", used.Tools[0].Name)
	})

	t.Run("unknown origin tool", func(t *testing.T) {
		runDSLExpectError(t, design("missing"), `selects unknown origin tool "missing"`)
	})
}

// TestAgentToolsetReferenceCycleFails verifies that mutually referencing
// cross-agent toolset exports produce a clear DSL error instead of silently
// materializing empty toolsets.
func TestAgentToolsetReferenceCycleFails(t *testing.T) {
	runDSLExpectError(t, func() {
		API("test", func() {})
		Service("svc-a", func() {
			Agent("agent-a", "desc", func() {
				Export(AgentToolset("svc-b", "agent-b", "shared-tools"))
			})
		})
		Service("svc-b", func() {
			Agent("agent-b", "desc", func() {
				Export(AgentToolset("svc-a", "agent-a", "shared-tools"))
			})
		})
	}, "cross-agent toolset reference cycle")
}

// requireConsumerUsedToolset returns the single Used toolset of the consumer
// agent declared as "consumer" in service "consumer-svc".
func requireConsumerUsedToolset(t *testing.T) *agentsexpr.ToolsetExpr {
	t.Helper()

	for _, a := range agentsexpr.Root.Agents {
		if a == nil || a.Service == nil || a.Service.Name != "consumer-svc" || a.Name != "consumer" {
			continue
		}
		require.NotNil(t, a.Used)
		require.Len(t, a.Used.Toolsets, 1)
		return a.Used.Toolsets[0]
	}
	t.Fatal(`agent "consumer" in service "consumer-svc" not found`)
	return nil
}
