package codegen_test

import (
	mcpdsl "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"testing"
)

const structuredContentContract = `package contract_test

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 members "example.com/contract/gen/members"
 generated "example.com/contract/gen/mcp_members"
 "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
 "github.com/modelcontextprotocol/go-sdk/mcp"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

type service struct{}
func (service) Member(_ context.Context, input *members.MemberListProjectOrMemberMe) (*members.MemberIdentityOrProjectMembers, error) {
 if _, ok := input.AsMemberListProject(); ok {
  result := members.NewMemberIdentityOrProjectMembersProjectMembers(members.ProjectMembers{&members.MemberIdentity{ID:"project-member"}})
  return &result, nil
 }
 result := members.NewMemberIdentityOrProjectMembersMemberIdentity(&members.MemberIdentity{ID:"member"})
 return &result, nil
}

func TestStructuredContentRevision(t *testing.T) {
 server, err := generated.NewSDKServer(service{}, &generated.SDKServerOptions{StreamableHTTP:&sdkbridge.StreamableHTTPOptions{Stateless:true,JSONResponse:true}})
 require.NoError(t,err)
 for _, version := range []string{"2025-11-25","2026-07-28"} {
  for _, tc := range []struct{name, arguments, expected string; array bool}{
   {"object",` + "`" + `{"action":"me"}` + "`" + `,` + "`" + `{"id":"member"}` + "`" + `,false},
   {"array",` + "`" + `{"action":"list_project","project_id":"10000000-0000-4000-8000-000000000001"}` + "`" + `,` + "`" + `[{"id":"project-member"}]` + "`" + `,true},
  } {
   t.Run(version+"/"+tc.name,func(t *testing.T){
    body := ` + "`" + `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + "`" + `+version+` + "`" + `","io.modelcontextprotocol/clientCapabilities":{}},"name":"member","arguments":` + "`" + `+tc.arguments+` + "`" + `}}` + "`" + `
    request := httptest.NewRequest(http.MethodPost,"http://localhost/mcp",strings.NewReader(body))
    request.Header.Set("Content-Type","application/json")
    request.Header.Set("Accept","application/json, text/event-stream")
    request.Header.Set("Mcp-Protocol-Version",version)
    request.Header.Set("Mcp-Method","tools/call")
    request.Header.Set("Mcp-Name","member")
    recorder := httptest.NewRecorder()
    server.Handler.ServeHTTP(recorder,request)
    require.Equal(t,http.StatusOK,recorder.Code,recorder.Body.String())
    var response struct{Error json.RawMessage; Result struct{IsError bool; Content []struct{Text string}; StructuredContent json.RawMessage}}
    require.NoError(t,json.Unmarshal(recorder.Body.Bytes(),&response))
    require.Empty(t,response.Error)
    assert.False(t,response.Result.IsError)
    require.Len(t,response.Result.Content,1)
    assert.JSONEq(t,tc.expected,response.Result.Content[0].Text)
    if tc.array && version=="2025-11-25" {
     assert.Empty(t,response.Result.StructuredContent,"legacy structuredContent must be object or absent")
    } else {
     assert.JSONEq(t,tc.expected,string(response.Result.StructuredContent))
    }
   })
  }
 }
}

func TestStructuredContentSDK(t *testing.T) {
 server,err:=generated.NewSDKServer(service{},&generated.SDKServerOptions{StreamableHTTP:&sdkbridge.StreamableHTTPOptions{Stateless:true,JSONResponse:true}})
 require.NoError(t,err)
 httpServer:=httptest.NewServer(server.Handler)
 defer httpServer.Close()
 client:=mcp.NewClient(&mcp.Implementation{Name:"probe",Version:"1"},nil)
 session,err:=client.Connect(t.Context(),&mcp.StreamableClientTransport{Endpoint:httpServer.URL},nil)
 require.NoError(t,err)
 defer func(){require.NoError(t,session.Close())}()
 result,err:=session.CallTool(t.Context(),&mcp.CallToolParams{Name:"member",Arguments:map[string]any{"action":"list_project","project_id":"10000000-0000-4000-8000-000000000001"}})
 require.NoError(t,err)
 assert.False(t,result.IsError)
 raw,err:=json.Marshal(result.StructuredContent)
 require.NoError(t,err)
 assert.JSONEq(t,` + "`" + `[{"id":"project-member"}]` + "`" + `,string(raw))
}
`

func TestGeneratedStructuredContentVersions(t *testing.T) {
	runGeneratedMCPContract(t, "example.com/contract", func() {
		API("contract", func() {})
		me := Type("MemberMe", func() {
			Attribute("action", String, func() { Enum("me") })
			Required("action")
			Meta("openapi:additionalProperties", "false")
		})
		list := Type("MemberListProject", func() {
			Attribute("action", String, func() { Enum("list_project") })
			Attribute("project_id", String, func() { Format(FormatUUID) })
			Required("action", "project_id")
			Meta("openapi:additionalProperties", "false")
		})
		identity := Type("MemberIdentity", func() {
			Attribute("id", String)
			Required("id")
		})
		roster := Type("ProjectMembers", ArrayOf(identity))
		Service("Members", func() {
			mcpdsl.MCP("contract", "1.0.0")
			Method("member", func() {
				Payload(OneOf(me, list), func() { Untagged() })
				Result(OneOf(identity, roster), func() { Untagged() })
				mcpdsl.Tool("member", "Read member identity or a project roster")
			})
		})
	}, structuredContentContract)
}
