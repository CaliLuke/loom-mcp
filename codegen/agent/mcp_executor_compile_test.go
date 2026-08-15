package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	mcpcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/mcp"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	loomgenerator "github.com/CaliLuke/loom/codegen/generator"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// TestGeneratedAgentDesignsCompile renders and compiles materially different
// agent designs. It guards against source-level assertions passing while the
// complete generated package graph is not valid Go.
func TestGeneratedAgentDesignsCompile(t *testing.T) {
	cases := []struct {
		name       string
		generate   func(*testing.T) []*gcodegen.File
		verify     func(*testing.T, []*gcodegen.File)
		moduleTest string
	}{
		{
			name: "FromMCP toolset",
			generate: func(t *testing.T) []*gcodegen.File {
				roots := runAliasedMCPDesign(t)
				return generateProductionFiles(t, roots, true)
			},
			verify: verifyFromMCPExecutor,
		},
		{
			name:     "method-backed MCP projection",
			generate: generateProjectedAgentDesign,
			verify: func(t *testing.T, files []*gcodegen.File) {
				require.NotEmpty(t, testhelpers.FileContent(t, files, "gen/assistant/agents/planner/lookup_tools/service_executor.go"))
			},
		},
		{
			name:     "registry-backed discovery",
			generate: generateRegistryAgentDesign,
			verify: func(t *testing.T, files []*gcodegen.File) {
				specs := testhelpers.FileContent(t, files, "gen/assistant/toolsets/data_tools/specs.go")
				require.Contains(t, specs, "func resolveLocalSchemaRef")
				require.Contains(t, specs, "func FreezeSpecs")
			},
		},
		{
			name:       "injected payload field",
			generate:   generateInjectedAgentDesign,
			moduleTest: injectedPayloadRuntimeTest,
			verify: func(t *testing.T, files []*gcodegen.File) {
				inject := testhelpers.FileContent(t, files, "gen/assistant/toolsets/lookup/inject.go")
				specs := testhelpers.FileContent(t, files, "gen/assistant/toolsets/lookup/specs.go")
				require.Contains(t, inject, "sessionIDValue := meta.SessionID")
				require.Contains(t, inject, "payload.SessionID = &sessionIDValue")
				require.Contains(t, inject, "payload.TurnID = turnIDValue")
				require.NotContains(t, specs, `"session_id"`)
				require.NotContains(t, specs, `"turn_id"`)
			},
		},
		{
			name:       "label-injected payload field",
			generate:   generateLabelInjectedAgentDesign,
			moduleTest: labelInjectedPayloadRuntimeTest,
			verify: func(t *testing.T, files []*gcodegen.File) {
				inject := testhelpers.FileContent(t, files, "gen/assistant/toolsets/lookup/inject.go")
				specs := testhelpers.FileContent(t, files, "gen/assistant/toolsets/lookup/specs.go")
				require.Contains(t, inject, `labelValue0, ok := labels["household_id"]`)
				require.Contains(t, inject, "payload.HouseholdID = &labelValue0")
				require.NotContains(t, specs, `"household_id"`)
			},
		},
		{
			name:     "payload-only bound method",
			generate: generatePayloadOnlyAgentDesign,
			verify: func(t *testing.T, files []*gcodegen.File) {
				transforms := testhelpers.FileContent(t, files, "gen/assistant/toolsets/notify/transforms.go")
				require.Contains(t, transforms, "func InitNotifyMethodPayload")
			},
		},
		{
			name:     "inherited bound method payload",
			generate: generateInheritedBoundMethodDesign,
			verify: func(t *testing.T, files []*gcodegen.File) {
				provider := testhelpers.FileContent(t, files, "gen/assistant/toolsets/profile/provider.go")
				require.Contains(t, provider, "methodIn := InitUpsertMethodPayload(args)")
				require.NotContains(t, provider, "methodIn := args")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.generate(t)
			tc.verify(t, files)
			moduleDir := writeGeneratedModule(t, files)
			if tc.moduleTest != "" {
				err := os.WriteFile(filepath.Join(moduleDir, "injection_runtime_test.go"), []byte(tc.moduleTest), 0o600)
				require.NoError(t, err)
			}

			build := exec.CommandContext(t.Context(), "go", "test", "./...")
			build.Dir = moduleDir
			build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			out, err := build.CombinedOutput()
			require.NoErrorf(t, err, "generated %s output does not compile:\n%s", tc.name, string(out))
		})
	}
}

func verifyFromMCPExecutor(t *testing.T, files []*gcodegen.File) {
	t.Helper()
	executor := testhelpers.FileContent(t, files, "gen/alpha/agents/scribe/calc_remote/mcp_executor.go")
	require.Contains(t, executor, "name := string(full)")
	require.Contains(t, executor, `raw = []byte("{}")`)
	require.Contains(t, executor, "decoded, err := pc.FromJSON(raw)")
	require.Contains(t, executor, "payload, err := pc.ToJSON(decoded)")
	require.NotContains(t, executor, "pc.ToJSON(call.Payload)",
		"call.Payload is raw JSON and must not be passed to the typed encoder")
}

func generateProjectedAgentDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	setupCompileEvalRoots(t, true)
	design := func() {
		API("assistant", func() {})
		payload := Type("LookupPayload", func() {
			Attribute("query", String)
			Required("query")
		})
		result := Type("LookupResult", func() {
			Attribute("answer", String)
			Required("answer")
		})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			Method("lookup", func() {
				Payload(payload)
				Result(result)
			})
			Agent("planner", "Planner", func() {
				Use("lookup_tools", func() {
					Tool("lookup", "Lookup", func() {
						Args(payload)
						Return(result)
						BindTo("assistant", "lookup")
						Expose(AgentRuntime, MCPSurface)
						MCPPlacement("assistant", "assistant-mcp")
					})
				})
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	return generateProductionFiles(t, []eval.Root{goaexpr.Root, agentsexpr.Root, mcpexpr.Root}, true)
}

func generateRegistryAgentDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	return generateCompileDesign(t, func() {
		API("assistant", func() {})
		registry := Registry("corp", func() {
			URL("https://registry.example.com")
			SyncInterval("5m")
			CacheTTL("1h")
		})
		tools := Toolset(FromRegistry(registry, "data-tools"))
		Service("assistant", func() {
			Agent("planner", "Planner", func() {
				Use(tools)
			})
		})
	})
}

func generateInjectedAgentDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	return generateCompileDesign(t, func() {
		API("assistant", func() {})
		payload := Type("LookupPayload", func() {
			Attribute("session_id", String)
			Attribute("turn_id", String, func() {
				Default("fallback")
			})
			Attribute("query", String)
			Required("session_id", "turn_id", "query")
		})
		Service("assistant", func() {
			Method("lookup", func() {
				Payload(payload)
				Result(String)
			})
			Agent("chat", "Chat agent", func() {
				Use("lookup", func() {
					Tool("lookup", "Lookup", func() {
						Args(payload)
						Return(String)
						BindTo("assistant", "lookup")
						Inject("session_id", "turn_id")
					})
				})
			})
		})
	})
}

const injectedPayloadRuntimeTest = `package fmcp_test

import (
	"testing"

	lookup "example.com/fmcp/gen/assistant/toolsets/lookup"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func TestInjectedPayloadRuntime(t *testing.T) {
	payload, err := lookup.DecodeLookup(
		[]byte("{\"query\":\"find me\"}"),
		runtime.ToolCallMeta{SessionID: "session-1", TurnID: "turn-1"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID == nil || *payload.SessionID != "session-1" {
		t.Fatalf("unexpected session ID: %#v", payload.SessionID)
	}
	if payload.TurnID != "turn-1" {
		t.Fatalf("unexpected turn ID: %q", payload.TurnID)
	}
}
`

func generateLabelInjectedAgentDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	return generateCompileDesign(t, func() {
		API("assistant", func() {})
		Service("assistant", func() {
			Agent("chat", "Chat agent", func() {
				Use("lookup", func() {
					Tool("lookup", "Lookup", func() {
						Args(func() {
							Attribute("household_id", String)
							Attribute("query", String)
							Required("household_id", "query")
						})
						Return(String)
						Inject("household_id")
					})
				})
			})
		})
	})
}

const labelInjectedPayloadRuntimeTest = `package fmcp_test

import (
	"testing"

	lookup "example.com/fmcp/gen/assistant/toolsets/lookup"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func TestLabelInjectedPayloadRuntime(t *testing.T) {
	payload, err := lookup.DecodeLookup(
		[]byte("{\"query\":\"find me\"}"),
		runtime.ToolCallMeta{},
		map[string]string{"household_id": "house-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if payload.HouseholdID == nil || *payload.HouseholdID != "house-1" {
		t.Fatalf("unexpected household ID: %#v", payload.HouseholdID)
	}
	if _, err := lookup.DecodeLookup([]byte("{\"query\":\"find me\"}"), runtime.ToolCallMeta{}, nil); err == nil {
		t.Fatal("expected missing label error")
	}
}
`

func generatePayloadOnlyAgentDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	return generateCompileDesign(t, func() {
		API("assistant", func() {})
		payload := Type("NotifyPayload", func() {
			Attribute("message", String)
			Required("message")
		})
		Service("assistant", func() {
			Method("notify", func() {
				Payload(payload)
			})
			Agent("chat", "Chat agent", func() {
				Use("notify", func() {
					Tool("notify", "Notify", func() {
						Args(payload)
						BindTo("assistant", "notify")
					})
				})
			})
		})
	})
}

func generateInheritedBoundMethodDesign(t *testing.T) []*gcodegen.File {
	t.Helper()
	setupCompileEvalRoots(t, false)
	design := func() {
		API("assistant", func() {})
		profile := Type("Profile", func() {
			Attribute("name", String)
			Required("name")
		})
		result := Type("ProfileResult", func() {
			Attribute("saved", Boolean)
			Required("saved")
		})
		Service("assistant", func() {
			Method("upsert", func() {
				Payload(profile)
				Result(result)
			})
			Agent("chat", "Chat agent", func() {
				Use("profile", func() {
					Tool("upsert", "Upsert profile", func() {
						BindTo("assistant", "upsert")
					})
				})
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	roots := []eval.Root{goaexpr.Root, agentsexpr.Root}
	return generateProductionFiles(t, roots, false)
}

func generateProductionFiles(t *testing.T, roots []eval.Root, withMCP bool) []*gcodegen.File {
	t.Helper()
	const genpkg = "example.com/fmcp/gen"
	require.NoError(t, codegen.Prepare(genpkg, roots))
	if withMCP {
		require.NoError(t, mcpcodegen.PrepareServices(genpkg, roots))
	}
	files := generateCoreAndAgentFiles(t, roots)
	if withMCP {
		var err error
		files, err = mcpcodegen.Generate(genpkg, roots, files)
		require.NoError(t, err)
	}
	return files
}

func setupCompileEvalRoots(t *testing.T, withMCP bool) {
	t.Helper()
	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))
	if withMCP {
		mcpexpr.Root = mcpexpr.NewRoot()
		require.NoError(t, eval.Register(mcpexpr.Root))
	}
}

func generateCoreAndAgentFiles(t *testing.T, roots []eval.Root) []*gcodegen.File {
	t.Helper()
	const genpkg = "example.com/fmcp/gen"
	files, err := loomgenerator.Service(genpkg, roots)
	require.NoError(t, err)
	transportFiles, err := loomgenerator.Transport(genpkg, roots)
	require.NoError(t, err)
	files = append(files, transportFiles...)
	files, err = codegen.Generate(genpkg, roots, files)
	require.NoError(t, err)
	return files
}

func generateCompileDesign(t *testing.T, design func()) []*gcodegen.File {
	t.Helper()
	setupCompileEvalRoots(t, false)
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	roots := []eval.Root{goaexpr.Root, agentsexpr.Root}
	return generateProductionFiles(t, roots, false)
}

// writeGeneratedModule materializes the generated .go files into a temp module
// whose go.mod mirrors this repository's dependency set and replaces loom-mcp
// with the local checkout so the build runs offline against the working tree.
func writeGeneratedModule(t *testing.T, files []*gcodegen.File) string {
	t.Helper()

	moduleDir := t.TempDir()
	for _, f := range files {
		// Render applies the production finalizers (gofmt + import pruning),
		// matching exactly what `loom gen` writes to disk.
		_, err := f.Render(moduleDir)
		require.NoErrorf(t, err, "render %s", f.Path)
	}

	repoRoot := repositoryRoot(t)
	goMod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod")) // #nosec G304 -- repoRoot is resolved from this test file's location.
	require.NoError(t, err)
	modContent := strings.Replace(
		string(goMod),
		"module github.com/CaliLuke/loom-mcp/v2",
		"module example.com/fmcp",
		1,
	)
	modContent += "\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\n" +
		"\nreplace github.com/CaliLuke/loom-mcp/v2 => " + repoRoot + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(modContent), 0o600)) // #nosec G703 -- moduleDir is a test-owned temp dir.

	goSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum")) // #nosec G304 -- repoRoot is resolved from this test file's location.
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.sum"), goSum, 0o600)) // #nosec G703 -- moduleDir is a test-owned temp dir.

	return moduleDir
}

// repositoryRoot resolves the loom-mcp repository root from this file's location.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve test file location")
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}
