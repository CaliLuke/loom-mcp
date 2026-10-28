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

const emptyPayloadContract = `package contract_test

import (
 "context"
 "encoding/json"
 "net/http/httptest"
 "sync/atomic"
 "testing"

 generated "example.com/empty-payload/gen/mcp_probe"
 "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
 "github.com/modelcontextprotocol/go-sdk/mcp"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

type probe struct{ calls atomic.Int64 }
func (p *probe) Ping(context.Context) (string,error) { p.calls.Add(1); return "pong",nil }

func TestEmptyPayloadAdmission(t *testing.T) {
 service := &probe{}
 server,err := generated.NewSDKServer(service,&generated.SDKServerOptions{StreamableHTTP:&sdkbridge.StreamableHTTPOptions{Stateless:true,JSONResponse:true}})
 require.NoError(t,err)
 httpServer := httptest.NewServer(server.Handler)
 defer httpServer.Close()
 client := mcp.NewClient(&mcp.Implementation{Name:"probe",Version:"1"},nil)
 session,err := client.Connect(t.Context(),&mcp.StreamableClientTransport{Endpoint:httpServer.URL},nil)
 require.NoError(t,err)
 defer func(){ require.NoError(t,session.Close()) }()
 catalog,err := session.ListTools(t.Context(),nil)
 require.NoError(t,err)
 require.Len(t,catalog.Tools,1)
 schema,err := json.Marshal(catalog.Tools[0].InputSchema)
 require.NoError(t,err)
 assert.JSONEq(t,` + "`" + `{"type":"object","properties":{},"additionalProperties":false}` + "`" + `,string(schema))
 for _,tc := range []struct{name string; args any; rejected bool}{
  {"omitted",nil,false},
  {"empty object",map[string]any{},false},
  {"unexpected key",map[string]any{"unexpected":"value"},true},
 } {
  t.Run(tc.name,func(t *testing.T){
   before := service.calls.Load()
   result,err := session.CallTool(t.Context(),&mcp.CallToolParams{Name:"ping",Arguments:tc.args})
   require.NoError(t,err)
   assert.Equal(t,tc.rejected,result.IsError)
   if tc.rejected {
    assert.Equal(t,before,service.calls.Load())
   } else {
    assert.Equal(t,before+1,service.calls.Load())
    require.Len(t,result.Content,1)
    assert.Equal(t,"pong",result.Content[0].(*mcp.TextContent).Text)
   }
  })
 }
}
`

func TestEmptyPayloadThroughSDK(t *testing.T) {
	runGeneratedMCPContract(t, "example.com/empty-payload", func() {
		API("probe", func() {})
		Service("probe", func() {
			mcpdsl.MCP("probe", "1.0.0")
			Method("ping", func() {
				Payload(func() {})
				Result(String)
				mcpdsl.Tool("ping", "Return pong without arguments")
			})
		})
	}, emptyPayloadContract)
}

// runGeneratedMCPContract exercises service and MCP generation in production order.
func runGeneratedMCPContract(t *testing.T, module string, design func(), contract string) {
	t.Helper()
	setupParityRoots(t)
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
	genpkg := module + "/gen"
	roots := []eval.Root{expr.Root, expr.GeneratedResultTypes, agentsexpr.Root, mcpexpr.Root}
	require.NoError(t, agentcodegen.Prepare(genpkg, roots))
	require.NoError(t, mcpcodegen.PrepareServices(genpkg, roots))
	files, err := loomgenerator.Service(genpkg, roots)
	require.NoError(t, err)
	files, err = mcpcodegen.Generate(genpkg, roots, files)
	require.NoError(t, err)
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	testhelpers.RunGeneratedModule(t, module, repo, files, map[string]string{"contract_test.go": contract})
}
