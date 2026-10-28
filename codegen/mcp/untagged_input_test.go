package codegen_test

import (
	"path/filepath"
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	mcpcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/mcp"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	mcpdsl "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	loomgenerator "github.com/CaliLuke/loom/codegen/generator"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestTopLevelUntaggedObjectInputThroughSDK(t *testing.T) {
	setupParityRoots(t)
	design := func() {
		API("flat input", func() {})
		configuration := Type("Configuration", func() {
			Attribute("expression", String, func() {
				MinLength(1)
				MaxLength(4096)
			})
			Meta("openapi:additionalProperties", "false")
		})
		read := Type("ReadInput", func() {
			Attribute("action", String, func() { Enum("read") })
			Attribute("id", String)
			Attribute("configuration", configuration)
			Required("action", "id")
			Meta("openapi:additionalProperties", "false")
		})
		write := Type("WriteInput", func() {
			Attribute("action", String, func() { Enum("write") })
			Attribute("id", String)
			Attribute("configuration", configuration)
			Attribute("name", String)
			Required("action", "id", "name")
			Meta("openapi:additionalProperties", "false")
		})
		Service("entries", func() {
			mcpdsl.MCP("entries", "1.0.0")
			Method("entry", func() {
				Payload(OneOf(read, write), func() { Untagged() })
				Result(func() {
					Attribute("name", String)
					Required("name")
				})
				mcpdsl.Tool("entry", "Read or write an entry")
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	const module = "example.com/flat-input"
	const genpkg = module + "/gen"
	roots := []eval.Root{expr.Root, expr.GeneratedResultTypes, agentsexpr.Root, mcpexpr.Root}
	require.NoError(t, agentcodegen.Prepare(genpkg, roots))
	require.NoError(t, mcpcodegen.PrepareServices(genpkg, roots))
	files, err := loomgenerator.Service(genpkg, roots)
	require.NoError(t, err)
	files, err = mcpcodegen.Generate(genpkg, roots, files)
	require.NoError(t, err)
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	testhelpers.RunGeneratedModule(t, module, repo, files, map[string]string{"contract_test.go": flatInputContractTest})
}

const flatInputContractTest = `package contract_test

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 entries "example.com/flat-input/gen/entries"
 generated "example.com/flat-input/gen/mcp_entries"
 "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

type entryService struct { calls int }

func (s *entryService) Entry(_ context.Context, input *entries.ReadInputOrWriteInput) (*entries.EntryResult, error) {
 s.calls++
 if read, ok := input.AsReadInput(); ok {
  return &entries.EntryResult{Name: "read:" + read.ID}, nil
 }
 write, ok := input.AsWriteInput()
 if !ok { panic("missing typed branch") }
 return &entries.EntryResult{Name: "write:" + write.ID + ":" + write.Name}, nil
}

type response struct {
 Result json.RawMessage
 Error *struct { Code int }
}

func rpc(t *testing.T, h http.Handler, method string, params any) response {
 t.Helper()
 body, err := json.Marshal(map[string]any{"jsonrpc":"2.0", "id":1, "method":method, "params":params})
 require.NoError(t, err)
 request := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
 request.Header.Set("Content-Type", "application/json")
 request.Header.Set("Accept", "application/json, text/event-stream")
 request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
 recorder := httptest.NewRecorder()
 h.ServeHTTP(recorder, request)
 require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
 var out response
 require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &out))
 return out
}

func TestFlatInputContract(t *testing.T) {
 service := &entryService{}
 server, err := generated.NewSDKServer(service, &generated.SDKServerOptions{
  StreamableHTTP: &sdkbridge.StreamableHTTPOptions{Stateless:true, JSONResponse:true},
 })
 require.NoError(t, err)
 discovery := rpc(t, server.Handler, "tools/list", map[string]any{})
 require.Nil(t, discovery.Error)
 var catalog struct { Tools []struct { Name string; InputSchema map[string]json.RawMessage } }
 require.NoError(t, json.Unmarshal(discovery.Result, &catalog))
 require.Len(t, catalog.Tools, 1)
 schema := catalog.Tools[0].InputSchema
 assert.JSONEq(t, "\"object\"", string(schema["type"]))
 var properties map[string]json.RawMessage
 require.NoError(t, json.Unmarshal(schema["properties"], &properties))
 assert.Contains(t, properties, "action")
 assert.JSONEq(t, "{\"type\":\"string\"}", string(properties["id"]))
 assert.JSONEq(t, "{\"type\":\"string\"}", string(properties["name"]))

 for _, tc := range []struct { name, input, result string }{
  {"read", "{\"action\":\"read\",\"id\":\"1\"}", "read:1"},
  {"write", "{\"action\":\"write\",\"id\":\"1\",\"name\":\"updated\"}", "write:1:updated"},
  {"read with configuration", "{\"action\":\"read\",\"id\":\"1\",\"configuration\":{\"expression\":\"valid\"}}", "read:1"},
  {"write with configuration", "{\"action\":\"write\",\"id\":\"1\",\"name\":\"updated\",\"configuration\":{\"expression\":\"valid\"}}", "write:1:updated"},
 } {
  t.Run(tc.name, func(t *testing.T) {
   before := service.calls
   result := rpc(t, server.Handler, "tools/call", map[string]any{"name":"entry", "arguments":json.RawMessage(tc.input)})
   require.Nil(t, result.Error)
   var output struct { StructuredContent struct { Name string }; IsError bool }
   require.NoError(t, json.Unmarshal(result.Result, &output))
   assert.False(t, output.IsError)
   assert.Equal(t, tc.result, output.StructuredContent.Name)
   assert.Equal(t, before+1, service.calls)
  })
 }
 var recovery string
 for _, input := range []string{
  "{\"action\":\"read\",\"id\":\"1\",\"configuration\":{\"expression\":\"\"}}",
  "{\"action\":\"write\",\"id\":\"1\",\"name\":\"updated\",\"configuration\":{\"expression\":\"\"}}",
  "{\"action\":\"unknown\",\"id\":\"1\"}",
  "{\"id\":\"1\"}",
  "{\"action\":\"read\"}",
  "{\"action\":\"write\",\"id\":\"1\"}",
  "{\"action\":\"read\",\"id\":\"1\",\"name\":\"wrong branch\"}",
  "{\"action\":\"read\",\"id\":\"1\",\"unknown\":true}",
 } {
  t.Run(input, func(t *testing.T) {
   before := service.calls
   result := rpc(t, server.Handler, "tools/call", map[string]any{"name":"entry", "arguments":json.RawMessage(input)})
   require.Nil(t, result.Error)
   var output struct { IsError bool; Content []struct { Type, Text string } }
   require.NoError(t, json.Unmarshal(result.Result, &output))
   assert.True(t, output.IsError)
   require.NotEmpty(t, output.Content)
   assert.Equal(t, "text", output.Content[0].Type)
   assert.Contains(t, output.Content[0].Text, "[invalid_params]")
   _, example, found := strings.Cut(output.Content[0].Text, "Example: ")
   require.True(t, found)
   recovery = example
   assert.Equal(t, before, service.calls)
  })
 }
 before := service.calls
 repaired := rpc(t, server.Handler, "tools/call", map[string]any{"name":"entry", "arguments":json.RawMessage(recovery)})
 require.Nil(t, repaired.Error)
 var repairResult struct { StructuredContent struct { Name string }; IsError bool }
 require.NoError(t, json.Unmarshal(repaired.Result, &repairResult))
 assert.False(t, repairResult.IsError, string(repaired.Result))
 assert.Equal(t, "read:example", repairResult.StructuredContent.Name)
 assert.Equal(t, before+1, service.calls)
}
`
