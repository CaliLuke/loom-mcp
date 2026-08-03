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

// TestLoomGen_GoSDKConformanceAgainstGeneratedJSONRPCTransport regenerates a
// projected-tools-only MCP design from scratch, compiles it, and drives the
// generated JSON-RPC transport with the official modelcontextprotocol/go-sdk
// client plus the generated JSON-RPC client with a bare http.Client — no
// compensating helpers. It proves, at the wire level:
//
//   - #124: a projected-tools-only design generates compilable code with a
//     working tools surface,
//   - #123: list requests with an omitted params key succeed,
//   - #127: the final streamed tools/call response arrives as a default
//     "message" SSE event that conformant clients process,
//   - #122: a projected tools/call with omitted arguments succeeds,
//   - #129/#130: the generated client sends the mandatory Accept header and
//     replays Mcp-Session-Id without wrapper Doers,
//   - #128: the transport rejects untrusted Origins with 403.
//   - #158: supplied unknown or stale session IDs receive transport-level 404
//     so the official client can recover by initializing a new session.
//
// The test always exercises freshly generated output in a temp module, so it
// does not depend on the (potentially stale) integration fixture gen/ trees.
func TestLoomGen_GoSDKConformanceAgainstGeneratedJSONRPCTransport(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	localLoomDir := currentLocalLoomReplace(t, repoRoot)

	fixtureRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(fixtureRoot, "design"), 0o750))

	goMod := `module example.com/conformance

go 1.27

require (
	github.com/CaliLuke/loom v1.0.7
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

// The tool declares its own arg/return shapes (mirroring the assistant
// fixture) so the toolset provider generates payload/result transforms. All
// args stay optional: the conformance flow calls the tool without arguments.
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

// The MCP server declares no method-level tools: its only tool arrives via
// MCPPlacement projection from an agent toolset (issue #124).
var _ = Service("demo", func() {
	MCP("demo", "0.1.0", ProtocolVersion("2025-06-18"))
	JSONRPC(func() { POST("/rpc") })

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
		cmd := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- test executes fixed toolchain commands with controlled args
		cmd.Dir = fixtureRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s\n%s", cmd.String(), string(out))
	}

	run("go", "mod", "tidy")
	run("go", "run", upstreampaths.LoomCLIPackage, "gen", "example.com/conformance/design")

	// The conformance test imports generated packages, so it can only join
	// the module after generation.
	require.NoError(t, os.WriteFile(filepath.Join(fixtureRoot, "conformance_test.go"), []byte(goSDKConformanceTestSource), 0o600))

	run("go", "mod", "tidy")
	run("go", "test", "./...")
}

// goSDKConformanceTestSource is compiled and executed inside the freshly
// generated temp module. It intentionally uses only the standard library plus
// the generated packages and the official go-sdk client: any compensating
// transport helper here would defeat the point of the conformance proof.
const goSDKConformanceTestSource = `package conformance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	demo "example.com/conformance/gen/demo"
	demojsonrpcc "example.com/conformance/gen/jsonrpc/mcp_demo/client"
	demojssvr "example.com/conformance/gen/jsonrpc/mcp_demo/server"
	mcpdemo "example.com/conformance/gen/mcp_demo"
	goahttp "github.com/CaliLuke/loom/http"
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

func newGeneratedServer(t *testing.T) *httptest.Server {
	t.Helper()
	adapter := mcpdemo.NewMCPAdapter(demoService{}, nil)
	endpoints := mcpdemo.NewEndpoints(adapter)
	mux := goahttp.NewMuxer()
	server := demojssvr.New(
		endpoints,
		mux,
		goahttp.RequestDecoder,
		goahttp.ResponseEncoder,
		func(ctx context.Context, _ http.ResponseWriter, err error) {
			t.Logf("jsonrpc server error: %v", err)
		},
	)
	demojssvr.Mount(mux, server)
	return httptest.NewServer(mux)
}

// TestGoSDKClientAgainstGeneratedJSONRPCTransport drives the generated
// JSON-RPC transport end to end with the official go-sdk client.
func TestGoSDKClientAgainstGeneratedJSONRPCTransport(t *testing.T) {
	server := newGeneratedServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "conformance-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             server.URL + "/rpc",
		HTTPClient:           &http.Client{Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("initialize via go-sdk client failed: %v", err)
	}
	defer session.Close()

	// tools/list with NO params key (the go-sdk omits it entirely).
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list without params failed: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "projected_lookup_tool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("projected tool missing from tools/list: %+v", tools.Tools)
	}

	// tools/call with omitted arguments on the projected tool.
	omitted, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "projected_lookup_tool"})
	if err != nil {
		t.Fatalf("tools/call with omitted arguments failed: %v", err)
	}
	if omitted.IsError {
		t.Fatalf("tools/call with omitted arguments returned tool error: %+v", omitted.Content)
	}
	structured, err := json.Marshal(omitted.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if !strings.Contains(string(structured), "42") {
		t.Fatalf("unexpected structured content for omitted arguments: %s", structured)
	}

	// tools/call with arguments still round-trips.
	withArgs, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "projected_lookup_tool",
		Arguments: map[string]any{"query": "ping"},
	})
	if err != nil {
		t.Fatalf("tools/call with arguments failed: %v", err)
	}
	if withArgs.IsError {
		t.Fatalf("tools/call with arguments returned tool error: %+v", withArgs.Content)
	}
	structured, err = json.Marshal(withArgs.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if !strings.Contains(string(structured), "answer:ping") {
		t.Fatalf("unexpected structured content: %s", structured)
	}
}

// TestGeneratedJSONRPCTransportRejectsStaleSessions proves an unknown session
// is rejected at the HTTP transport before JSON-RPC routing, after which the
// official client can recover by initializing a fresh session.
func TestGeneratedJSONRPCTransportRejectsStaleSessions(t *testing.T) {
	server := newGeneratedServer(t)
	defer server.Close()

	body := ` + "`" + `{"jsonrpc":"2.0","id":"list-1","method":"tools/list","params":{}}` + "`" + `
	req, err := http.NewRequest(http.MethodPost, server.URL+"/rpc", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build stale-session request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "stale-after-restart")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send stale-session request: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close stale-session response: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected HTTP 404 for stale session, got %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "recovery-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             server.URL + "/rpc",
		HTTPClient:           &http.Client{Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("initialize fresh session after stale-session 404: %v", err)
	}
	defer session.Close()
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("fresh session failed after stale-session recovery: %v", err)
	}
}

// TestGeneratedJSONRPCClientNeedsNoTransportWrappers proves the generated
// client handles Accept, Mcp-Session-Id, and the protocol version header on
// its own when driven through a bare http.Client.
func TestGeneratedJSONRPCClientNeedsNoTransportWrappers(t *testing.T) {
	server := newGeneratedServer(t)
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := demojsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		&http.Client{Timeout: 10 * time.Second},
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.Initialize()(ctx, &mcpdemo.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo:      &mcpdemo.ClientInfo{Name: "generated-client", Version: "1.0.0"},
	}); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	rawList, err := client.ToolsList()(ctx, &mcpdemo.ToolsListPayload{})
	if err != nil {
		t.Fatalf("tools/list after initialize failed (session not replayed?): %v", err)
	}
	list, ok := rawList.(*mcpdemo.ToolsListResult)
	if !ok || len(list.Tools) == 0 {
		t.Fatalf("unexpected tools/list result: %#v", rawList)
	}

	rawStream, err := client.ToolsCall()(ctx, &mcpdemo.ToolsCallPayload{Name: "projected_lookup_tool"})
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}
	stream, ok := rawStream.(*demojsonrpcc.ToolsCallClientStream)
	if !ok {
		t.Fatalf("unexpected tools/call stream type: %T", rawStream)
	}
	result, err := stream.Recv(ctx)
	if err != nil {
		t.Fatalf("tools/call stream recv failed: %v", err)
	}
	if result.IsError != nil && *result.IsError {
		t.Fatalf("tools/call returned tool error: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tools/call returned no content")
	}
}

// TestGeneratedJSONRPCTransportValidatesOrigin proves the transport rejects
// untrusted Origins with 403 and accepts same-origin requests.
func TestGeneratedJSONRPCTransportValidatesOrigin(t *testing.T) {
	server := newGeneratedServer(t)
	defer server.Close()

	post := func(origin string) int {
		t.Helper()
		body := ` + "`" + `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"origin","version":"1.0.0"}}}` + "`" + `
		req, err := http.NewRequest(http.MethodPost, server.URL+"/rpc", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post initialize: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("https://evil.example.com"); code != http.StatusForbidden {
		t.Fatalf("expected 403 for untrusted Origin, got %d", code)
	}
	if code := post(server.URL); code == http.StatusForbidden {
		t.Fatalf("same-origin request must not be rejected")
	}
}
`
