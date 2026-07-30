package dsl_test

import (
	"testing"
	"time"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestAgentDSLExample(t *testing.T) {
	runDSL(t, func() {
		API("example", func() {})
		Service("docs", func() {
			Agent("docs-agent", "Agent for managing documentation workflows", func() {
				Use("summarization-tools", func() {
					Tool("document-summarizer", "Summarize documents", func() {})
				})
				Export("text-processing-suite", func() {
					Tool("doc-abstractor", "Create document abstracts", func() {})
				})
				RunPolicy(func() {
					DefaultCaps(
						MaxToolCalls(5),
						MaxConsecutiveFailedToolCalls(2),
					)
					TimeBudget("30s")
					InterruptsAllowed(true)
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	agent := agentsexpr.Root.Agents[0]
	require.Equal(t, "docs-agent", agent.Name)
	require.Equal(t, "docs", agent.Service.Name)
	require.NotNil(t, agent.RunPolicy)
	require.NotNil(t, agent.Used)
	require.NotNil(t, agent.Exported)
}

func TestGlobalToolsetRegisters(t *testing.T) {
	runDSL(t, func() {
		Toolset("global-tools", func() {
			Tool("summarize", "Summarize text", func() {})
		})
	})

	require.Len(t, agentsexpr.Root.Toolsets, 1)
	ts := agentsexpr.Root.Toolsets[0]
	require.Equal(t, "global-tools", ts.Name)
	require.Len(t, ts.Tools, 1)
	require.Equal(t, "summarize", ts.Tools[0].Name)
}

func TestRunPolicyDefaults(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("tasks", func() {
			Agent("planner", "Planner agent", func() {
				RunPolicy(func() {
					DefaultCaps(MaxToolCalls(3))
					TimeBudget("45s")
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.NotNil(t, policy.DefaultCaps)
	require.Equal(t, 3, policy.DefaultCaps.MaxToolCalls)
	require.Equal(t, 45*time.Second, policy.TimeBudget)
}

func TestRunPolicyRejectsNonPositiveCapsAndBudgets(t *testing.T) {
	cases := []struct {
		name string
		dsl  func()
		err  string
	}{
		{
			name: "negative max tool calls",
			dsl: func() {
				DefaultCaps(MaxToolCalls(-5))
			},
			err: "MaxToolCalls requires n > 0",
		},
		{
			name: "zero max tool calls",
			dsl: func() {
				DefaultCaps(MaxToolCalls(0))
			},
			err: "MaxToolCalls requires n > 0",
		},
		{
			name: "negative max consecutive failed tool calls",
			dsl: func() {
				DefaultCaps(MaxConsecutiveFailedToolCalls(-1))
			},
			err: "MaxConsecutiveFailedToolCalls requires n > 0",
		},
		{
			name: "zero max consecutive failed tool calls",
			dsl: func() {
				DefaultCaps(MaxConsecutiveFailedToolCalls(0))
			},
			err: "MaxConsecutiveFailedToolCalls requires n > 0",
		},
		{
			name: "negative time budget",
			dsl: func() {
				TimeBudget("-3s")
			},
			err: "TimeBudget requires duration > 0",
		},
		{
			name: "zero time budget",
			dsl: func() {
				TimeBudget("0s")
			},
			err: "TimeBudget requires duration > 0",
		},
		{
			name: "negative budget",
			dsl: func() {
				Budget("-3s")
			},
			err: "Budget requires duration > 0",
		},
		{
			name: "zero plan timeout",
			dsl: func() {
				Plan("0s")
			},
			err: "Plan requires duration > 0",
		},
		{
			name: "negative tool timeout",
			dsl: func() {
				Tools("-3s")
			},
			err: "Tools requires duration > 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, func() {
				API("test", func() {})
				Service("tasks", func() {
					Agent("planner", "Planner agent", func() {
						RunPolicy(tc.dsl)
					})
				})
			}, tc.err)
		})
	}
}

func TestRunPolicyRetryAndReflect(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("tasks", func() {
			Agent("planner", "Planner agent", func() {
				RunPolicy(func() {
					RetryAndReflect(MaxRetries(2), ErrorIfRetryExceeded(true))
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.NotNil(t, policy.RetryAndReflect)
	require.Equal(t, 2, policy.RetryAndReflect.MaxRetries)
	require.True(t, policy.RetryAndReflect.ErrorIfRetryExceeded)
}

func TestWorkflowCompositionDSL(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("tasks", func() {
			Agent("planner", "Planner agent", func() {
				Workflow(func() {
					Step("draft", "writer.draft", `{"topic":"loom"}`)
					Step("review", "reviewer.review", `{"strict":true}`)
					FinalMessage("workflow complete")
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	workflow := agentsexpr.Root.Agents[0].Workflow
	require.NotNil(t, workflow)
	require.Len(t, workflow.Steps, 2)
	require.Equal(t, "draft", workflow.Steps[0].Name)
	require.Equal(t, "writer.draft", workflow.Steps[0].Tool)
	require.JSONEq(t, `{"topic":"loom"}`, workflow.Steps[0].Payload)
	require.Equal(t, "workflow complete", workflow.FinalMessage)
}

func TestToolsetReferenceReuse(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		shared := Toolset("shared-tools", func() {
			Tool("ping", "Ping helper", func() {})
		})
		Service("ops", func() {
			Agent("watcher", "Watches", func() {
				Use(shared)
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	agent := agentsexpr.Root.Agents[0]
	require.NotNil(t, agent.Used)
	require.Len(t, agent.Used.Toolsets, 1)
	require.Equal(t, "shared-tools", agent.Used.Toolsets[0].Name)
}

func TestToolsetReferenceSubsetSelectsOriginTools(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		shared := Toolset("shared-tools", func() {
			Tool("ping", "Ping helper", func() {
				Args(func() {
					Attribute("message", String)
					Required("message")
				})
			})
			Tool("pong", "Pong helper", func() {})
		})
		Service("ops", func() {
			Agent("watcher", "Watches", func() {
				Use(shared, func() {
					Tool("ping")
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	used := agentsexpr.Root.Agents[0].Used.Toolsets[0]
	require.Equal(t, "shared-tools", used.Name)
	require.Len(t, used.Tools, 1)
	require.Equal(t, "ping", used.Tools[0].Name)
	require.Equal(t, "Ping helper", used.Tools[0].Description)
	require.NotNil(t, used.Tools[0].Args)
}

func TestToolsetReferenceSubsetRejectsUnknownOriginTool(t *testing.T) {
	runDSLExpectError(t, func() {
		API("test", func() {})
		shared := Toolset("shared-tools", func() {
			Tool("ping", "Ping helper", func() {})
		})
		Service("ops", func() {
			Agent("watcher", "Watches", func() {
				Use(shared, func() {
					Tool("missing")
				})
			})
		})
	}, `selects unknown origin tool "missing" from toolset "shared-tools"`)
}

func TestBindToSelfServiceMethod(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Method("GetX", func() {
				Payload(String)
				Result(String)
			})
			Agent("agent", "desc", func() {
				Use("ts", func() {
					Tool("tool", "t", func() {
						BindTo("GetX")
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	a := agentsexpr.Root.Agents[0]
	require.NotNil(t, a.Used)
	require.Len(t, a.Used.Toolsets, 1)
	ts := a.Used.Toolsets[0]
	require.Len(t, ts.Tools, 1)
	tool := ts.Tools[0]
	require.NotNil(t, tool.Method, "BindTo should resolve to MethodExpr")
	require.Equal(t, "GetX", tool.Method.Name)
}

func TestBindToCrossServiceMethod(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svcA", func() {
			Agent("agent", "desc", func() {
				Use("ts", func() {
					Tool("tool", "t", func() {
						BindTo("svcB", "GetY")
					})
				})
			})
		})
		Service("svcB", func() {
			Method("GetY", func() {
				Payload(String)
				Result(String)
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	a := agentsexpr.Root.Agents[0]
	ts := a.Used.Toolsets[0]
	tool := ts.Tools[0]
	require.NotNil(t, tool.Method)
	require.Equal(t, "GetY", tool.Method.Name)
	require.Equal(t, "svcB", tool.Method.Service.Name)
}

func TestAgentToolsetCrossServiceReference(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		// Service A exports a toolset
		Service("svcA", func() {
			Agent("agentA", "desc", func() {
				Export("exported", func() {
					Tool("t1", "tool one", func() {})
				})
			})
		})
		// Service B consumes it via AgentToolset
		Service("svcB", func() {
			Agent("agentB", "desc", func() {
				Use(AgentToolset("svcA", "agentA", "exported"))
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 2)
	// Find consumer agent (svcB.agentB)
	var consumer *agentsexpr.AgentExpr
	for _, a := range agentsexpr.Root.Agents {
		if a.Service != nil && a.Service.Name == "svcB" && a.Name == "agentB" {
			consumer = a
			break
		}
	}
	require.NotNil(t, consumer)
	require.NotNil(t, consumer.Used)
	require.Len(t, consumer.Used.Toolsets, 1)
	ts := consumer.Used.Toolsets[0]
	require.NotNil(t, ts.Origin, "AgentToolset should preserve origin")
	// Origin should point to the exported toolset on svcA.agentA.
	var provider *agentsexpr.AgentExpr
	for _, a := range agentsexpr.Root.Agents {
		if a.Service != nil && a.Service.Name == "svcA" && a.Name == "agentA" {
			provider = a
			break
		}
	}
	require.NotNil(t, provider)
	require.NotNil(t, provider.Exported)
	require.Len(t, provider.Exported.Toolsets, 1)
	exported := provider.Exported.Toolsets[0]
	require.Equal(t, exported, ts.Origin)
}

func TestAgentToolsetCrossServiceReferenceIsDeclarationOrderIndependent(t *testing.T) {
	cases := []struct {
		name          string
		providerFirst bool
	}{
		{name: "provider first", providerFirst: true},
		{name: "consumer first", providerFirst: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := func() {
				Service("svcA", func() {
					Agent("agentA", "desc", func() {
						Export("exported", func() {
							Tool("t1", "tool one", func() {})
						})
					})
				})
			}
			consumer := func() {
				Service("svcB", func() {
					Agent("agentB", "desc", func() {
						Use(AgentToolset("svcA", "agentA", "exported"))
					})
				})
			}

			runDSL(t, func() {
				API("test", func() {})
				if tc.providerFirst {
					provider()
					consumer()
					return
				}
				consumer()
				provider()
			})

			var consumerAgent, providerAgent *agentsexpr.AgentExpr
			for _, agent := range agentsexpr.Root.Agents {
				switch {
				case agent.Service.Name == "svcA" && agent.Name == "agentA":
					providerAgent = agent
				case agent.Service.Name == "svcB" && agent.Name == "agentB":
					consumerAgent = agent
				}
			}
			require.NotNil(t, providerAgent)
			require.NotNil(t, consumerAgent)
			require.NotNil(t, providerAgent.Exported)
			require.NotNil(t, consumerAgent.Used)
			require.Len(t, consumerAgent.Used.Toolsets, 1)
			require.Equal(t, providerAgent.Exported.Toolsets[0], consumerAgent.Used.Toolsets[0].Origin)
			require.Len(t, consumerAgent.Used.Toolsets[0].Tools, 1)
			require.Equal(t, "t1", consumerAgent.Used.Toolsets[0].Tools[0].Name)
		})
	}
}

func TestProviderInference_LocalAndMCP(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		var SearchSuite = Toolset(FromMCP("svc", "search"))
		Service("svc", func() {
			MCP("search", "1.0.0")
			Agent("a", "desc", func() {
				Use("local", func() { Tool("x", "", func() {}) })
				Use(SearchSuite)
			})
		})
	})
	require.Len(t, agentsexpr.Root.Agents, 1)
	a := agentsexpr.Root.Agents[0]
	require.Len(t, a.Used.Toolsets, 2)
	// Order matches declaration: local then MCP.
	local := a.Used.Toolsets[0]
	mcp := a.Used.Toolsets[1]
	// Local toolset has no Provider (or Provider.Kind != ProviderMCP)
	require.True(t, local.Provider == nil || local.Provider.Kind != agentsexpr.ProviderMCP)
	// MCP toolset has Provider with ProviderMCP kind
	require.NotNil(t, mcp.Provider)
	require.Equal(t, agentsexpr.ProviderMCP, mcp.Provider.Kind)
	require.Equal(t, "svc", mcp.Provider.MCPService)
	require.Equal(t, "search", mcp.Provider.MCPToolset)
}

func TestFromSkillsProvider(t *testing.T) {
	runDSL(t, func() {
		Toolset(FromSkills(".agents/skills", SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall)))
	})

	require.Len(t, agentsexpr.Root.Toolsets, 1)
	ts := agentsexpr.Root.Toolsets[0]
	require.Equal(t, "skills", ts.Name)
	require.NotNil(t, ts.Provider)
	require.Equal(t, agentsexpr.ProviderSkills, ts.Provider.Kind)
	require.Equal(t, []string{".agents/skills"}, ts.Provider.SkillRoots)
	require.Equal(t, agentsexpr.SkillPreloadOnStart, ts.Provider.SkillPreload)
	require.Equal(t, agentsexpr.SkillReloadPerCall, ts.Provider.SkillReload)
}

func runDSL(t *testing.T, dsl func()) {
	t.Helper()

	resetDSLRoots(t)

	require.True(t, eval.Execute(dsl, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
}

func runDSLExpectError(t *testing.T, dsl func(), want string) {
	t.Helper()

	resetDSLRoots(t)

	executed := eval.Execute(dsl, nil)
	err := eval.RunDSL()
	if err != nil {
		require.ErrorContains(t, err, want)
		return
	}
	require.False(t, executed)
	require.Contains(t, eval.Context.Error(), want)
}

func resetDSLRoots(t *testing.T) {
	t.Helper()

	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)

	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))

	mcpexpr.Root = mcpexpr.NewRoot()
	require.NoError(t, eval.Register(mcpexpr.Root))

	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))

	goaexpr.Root.API = goaexpr.NewAPIExpr("test", func() {})
	goaexpr.Root.API.Servers = []*goaexpr.ServerExpr{goaexpr.Root.API.DefaultServer()}
}

// TestPassthroughWithServiceAndMethodNames verifies Passthrough works with
// service name and method name strings.
func TestPassthroughWithServiceAndMethodNames(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("logging", func() {
			Method("LogMessage", func() {
				Payload(String)
				Result(String)
			})
			Agent("agent", "desc", func() {
				Export("logging-tools", func() {
					Tool("log_message", "Log a message", func() {
						Args(String)
						Return(String)
						Passthrough("log_message", "logging", "LogMessage")
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	a := agentsexpr.Root.Agents[0]
	require.NotNil(t, a.Exported)
	require.Len(t, a.Exported.Toolsets, 1)
	ts := a.Exported.Toolsets[0]
	require.Len(t, ts.Tools, 1)
	tool := ts.Tools[0]
	require.NotNil(t, tool.ExportPassthrough, "Passthrough should set ExportPassthrough")
	require.Equal(t, "logging", tool.ExportPassthrough.TargetService)
	require.Equal(t, "LogMessage", tool.ExportPassthrough.TargetMethod)
}

// TestPassthroughWithMethodExpr verifies Passthrough works with a MethodExpr reference.
func TestPassthroughWithMethodExpr(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("logging", func() {
			var logMethod *goaexpr.MethodExpr
			Method("LogMessage", func() {
				Payload(String)
				Result(String)
			})
			// Get the method expression after it's created
			logMethod = goaexpr.Root.Service("logging").Method("LogMessage")
			Agent("agent", "desc", func() {
				Export("logging-tools", func() {
					Tool("log_message", "Log a message", func() {
						Args(String)
						Return(String)
						Passthrough("log_message", logMethod)
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	a := agentsexpr.Root.Agents[0]
	require.NotNil(t, a.Exported)
	require.Len(t, a.Exported.Toolsets, 1)
	ts := a.Exported.Toolsets[0]
	require.Len(t, ts.Tools, 1)
	tool := ts.Tools[0]
	require.NotNil(t, tool.ExportPassthrough, "Passthrough should set ExportPassthrough")
	require.Equal(t, "logging", tool.ExportPassthrough.TargetService)
	require.Equal(t, "LogMessage", tool.ExportPassthrough.TargetMethod)
}

// TestTimingConfiguration verifies Timing DSL with Budget, Plan, and Tools.
func TestTimingConfiguration(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					Timing(func() {
						Budget("10m")
						Plan("45s")
						Tools("2m")
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.Equal(t, 10*time.Minute, policy.TimeBudget)
	require.Equal(t, 45*time.Second, policy.PlanTimeout)
	require.Equal(t, 2*time.Minute, policy.ToolTimeout)
}

// TestHistoryKeepRecentTurns verifies History with KeepRecentTurns.
func TestHistoryKeepRecentTurns(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					History(func() {
						KeepRecentTurns(20)
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.NotNil(t, policy.History)
	require.Equal(t, agentsexpr.HistoryModeKeepRecent, policy.History.Mode)
	require.Equal(t, 20, policy.History.KeepRecent)
}

// TestHistoryCompress verifies History with Compress.
func TestHistoryCompress(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					History(func() {
						CompressAtMaxInputTokens(120000)
						KeepMaxInputTokens(40000)
						KeepMaxTurns(10)
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.NotNil(t, policy.History)
	require.Equal(t, agentsexpr.HistoryModeCompress, policy.History.Mode)
	require.Equal(t, 120000, policy.History.CompressAtMaxInputTokens)
	require.Equal(t, 40000, policy.History.KeepMaxInputTokens)
	require.Equal(t, 10, policy.History.KeepMaxTurns)
}

// TestCacheConfiguration verifies Cache with AfterSystem and AfterTools.
func TestCacheConfiguration(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					Cache(func() {
						AfterSystem()
						AfterTools()
					})
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.NotNil(t, policy.Cache)
	require.True(t, policy.Cache.AfterSystem)
	require.True(t, policy.Cache.AfterTools)
}

// TestInterruptsAllowed verifies InterruptsAllowed DSL.
func TestInterruptsAllowed(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					InterruptsAllowed(true)
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.True(t, policy.InterruptsAllowed)
}

// TestOnMissingFields verifies OnMissingFields DSL.
func TestOnMissingFields(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					InterruptsAllowed(true)
					OnMissingFields("await_clarification")
				})
			})
		})
	})

	require.Len(t, agentsexpr.Root.Agents, 1)
	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy)
	require.Equal(t, "await_clarification", policy.OnMissingFields)
}
