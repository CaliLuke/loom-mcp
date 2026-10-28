package codegen_test

import (
	"fmt"
	"testing"

	mcpdsl "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

const uuidRecoveryContract = `package contract_test

import (
 "context"
 "encoding/json"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"

 service "example.com/recovery-repro/gen/example"
 generated "example.com/recovery-repro/gen/mcp_example"
 "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
 "github.com/modelcontextprotocol/go-sdk/mcp"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

type probe struct{calls atomic.Int64}
func (p *probe) Submit(_ context.Context,input *service.SubmitPayload)(string,error){p.calls.Add(1);return "ok",nil}

func TestUUIDRecoveryExample(t *testing.T){
 service:= &probe{}
 server,err:=generated.NewSDKServer(service,&generated.SDKServerOptions{StreamableHTTP:&sdkbridge.StreamableHTTPOptions{Stateless:true,JSONResponse:true}})
 require.NoError(t,err)
 httpServer:=httptest.NewServer(server.Handler)
 defer httpServer.Close()
 client:=mcp.NewClient(&mcp.Implementation{Name:"probe",Version:"1"},nil)
 session,err:=client.Connect(t.Context(),&mcp.StreamableClientTransport{Endpoint:httpServer.URL},nil)
 require.NoError(t,err)
 defer func(){require.NoError(t,session.Close())}()
 catalog,err:=session.ListTools(t.Context(),nil)
 require.NoError(t,err)
 require.Len(t,catalog.Tools,1)
 schema,err:=json.Marshal(catalog.Tools[0].InputSchema)
 require.NoError(t,err)
 invalid,err:=session.CallTool(t.Context(),&mcp.CallToolParams{Name:"submit",Arguments:json.RawMessage(invalidArguments)})
 require.NoError(t,err)
 require.True(t,invalid.IsError)
 assert.Zero(t,service.calls.Load())
 require.NotEmpty(t,invalid.Content)
 if !wantRecoveryExample {
  assert.NotContains(t,invalid.Content[0].(*mcp.TextContent).Text,"Example:")
  return
 }
 _,example,found:=strings.Cut(invalid.Content[0].(*mcp.TextContent).Text,"Example: ")
 require.True(t,found)
 assert.NoError(t,sdkbridge.ValidateToolArguments([]byte(example),string(schema)))
 repaired,err:=session.CallTool(t.Context(),&mcp.CallToolParams{Name:"submit",Arguments:json.RawMessage(example)})
 require.NoError(t,err)
 assert.False(t,repaired.IsError)
 assert.Equal(t,int64(1),service.calls.Load())
}
`

func TestUUIDRecoveryThroughSDK(t *testing.T) {
	const uuidPattern = "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
	for _, tc := range []struct {
		name, pattern, arguments string
		tagged, wantExample      bool
	}{
		{"uuid", uuidPattern, "{}", false, true},
		{"restricted UUID", "^abcd", "{}", false, false},
		{"restricted union branch", "^abcd", `{"request":{"type":"restricted","value":{}}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runGeneratedMCPContract(t, "example.com/recovery-repro", func() {
				API("example", func() {})
				fields := func(pattern string) {
					Attribute("requestKey", String, func() {
						Format(FormatUUID)
						Pattern(pattern)
					})
					Required("requestKey")
				}
				normal := Type("normal", func() {
					fields(uuidPattern)
					Meta("oneof:type:tag", "normal")
				})
				restricted := Type("restricted", func() {
					fields(tc.pattern)
					Meta("oneof:type:tag", "restricted")
				})
				Service("example", func() {
					mcpdsl.MCP("example", "1")
					Method("submit", func() {
						Payload(func() {
							if tc.tagged {
								Attribute("request", OneOf(normal, restricted))
								Required("request")
							} else {
								fields(tc.pattern)
							}
						})
						Result(String)
						mcpdsl.Tool("submit", "Submit a request")
					})
				})
			}, uuidRecoveryContract+fmt.Sprintf("\nconst wantRecoveryExample = %t\nconst invalidArguments = %q\n", tc.wantExample, tc.arguments))
		})
	}
}
