package framework

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/internal/upstreampaths"
	"github.com/stretchr/testify/require"
)

// TestLoomGen_GoSDKConformance regenerates a projected-tools-only MCP design
// from scratch and drives its SDKServer with the official MCP Go SDK client.
// The generated tree must not contain Loom-owned MCP JSON-RPC transports.
func TestLoomGen_GoSDKConformance(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	localLoomDir := currentLocalLoomReplace(t, repoRoot)

	fixtureRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(fixtureRoot, "design"), 0o750))

	goMod := `module example.com/conformance

go 1.27.0

require (
	github.com/CaliLuke/loom v1.9.0-alpha.8
	github.com/CaliLuke/loom-mcp/v2 v2.0.0
)

replace github.com/CaliLuke/loom-mcp/v2 => ` + repoRoot + `
`
	if localLoomDir != "" {
		goMod += `
replace github.com/CaliLuke/loom => ` + localLoomDir + `
`
	}
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "go.mod"), []byte(goMod), 0o600))

	design := `package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

var LookupPayload = Type("LookupPayload", func() {
	Attribute("query", String)
})

var LookupResult = Type("LookupResult", func() {
	Attribute("answer", String)
	Required("answer")
})

var LookupToolArgs = Type("LookupToolArgs", func() {
	Attribute("query", String, "Lookup query")
})

var LookupToolReturn = Type("LookupToolReturn", func() {
	Attribute("answer", String, "Lookup answer")
	Required("answer")
})

var _ = API("conformance", func() {
	Title("Conformance")
})

var _ = Service("demo", func() {
	MCP("demo", "0.1.0")

	Method("lookup", func() {
		Payload(LookupPayload)
		Result(LookupResult)
	})

	Agent("planner", "Planner", func() {
		Use("lookup_tools", func() {
			Tool("projected_lookup_tool", "Lookup answers", func() {
				Args(LookupToolArgs)
				Return(LookupToolReturn)
				BindTo("demo", "lookup")
				Expose(AgentRuntime, MCPSurface)
				MCPPlacement("demo", "demo")
			})
		})
	})
})
`
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "design", "design.go"), []byte(design), 0o600))

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- controlled test toolchain command
		cmd.Dir = fixtureRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s\n%s", cmd.String(), string(out))
	}

	run("go", "mod", "tidy")
	run("go", "run", "-mod=mod", upstreampaths.LoomCLIPackage, "gen", "example.com/conformance/design")
	_, err := os.Stat(filepath.Join(fixtureRoot, "gen", "jsonrpc"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "conformance_test.go"), []byte(goSDKConformanceTestSource), 0o600))
	run("go", "mod", "tidy")
	run("go", "test", "./...")
}

const goSDKConformanceTestSource = `package conformance_test

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	demo "example.com/conformance/gen/demo"
	mcpdemo "example.com/conformance/gen/mcp_demo"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type demoService struct{}

func (demoService) Lookup(ctx context.Context, p *demo.LookupPayload) (*demo.LookupResult, error) {
	answer := "42"
	if p != nil && p.Query != nil {
		answer = "answer:" + *p.Query
	}
	return &demo.LookupResult{Answer: answer}, nil
}

func passthroughToolCall(ctx context.Context, info mcpdemo.ToolCallInterceptorInfo, payload *mcpdemo.ToolsCallPayload, next mcpdemo.ToolCallHandler) (*mcpdemo.ToolsCallResult, error) {
	return next(ctx, payload)
}

func TestOfficialSDKClientAgainstGeneratedSDKServer(t *testing.T) {
	generated, err := mcpdemo.NewSDKServer(demoService{}, &mcpdemo.SDKServerOptions{
		Adapter: &mcpdemo.MCPAdapterOptions{
			ToolCallInterceptors: []mcpdemo.ToolCallInterceptor{passthroughToolCall},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(generated.Handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "conformance-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		found = found || tool.Name == "projected_lookup_tool"
	}
	if !found {
		t.Fatalf("projected tool missing: %+v", tools.Tools)
	}

	for _, call := range []*sdkmcp.CallToolParams{{Name: "projected_lookup_tool"}} {
		result, err := session.CallTool(ctx, call)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("tool error: %+v", result.Content)
		}
		structured, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		want := "42"
		if !strings.Contains(string(structured), want) {
			t.Fatalf("structured content %s does not contain %q", structured, want)
		}
	}
}
`
