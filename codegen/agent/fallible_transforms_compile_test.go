package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

// TestGeneratedFallibleTransforms prevents failed custom conversions from
// reaching a method or being published as successful tool results.
func TestGeneratedFallibleTransforms(t *testing.T) {
	files := generateCompileDesign(t, func() {
		API("fallible", func() {})
		customChild := Type("CustomChild", func() {
			Attribute("id", String, func() { Meta("struct:field:type", "uuid.UUID", "github.com/google/uuid") })
			Required("id")
		})
		plainChild := Type("PlainChild", func() { Attribute("id", String); Required("id") })
		defaultChild := Type("DefaultChild", func() {
			Attribute("id", String, func() { Meta("struct:field:type", "uuid.UUID", "github.com/google/uuid"); Default("invalid") })
		})
		custom := Type("Custom", func() {
			Attribute("id", String, func() { Meta("struct:field:type", "uuid.UUID", "github.com/google/uuid") })
			Attribute("children", ArrayOf(customChild))
			Required("id")
		})
		plain := Type("Plain", func() {
			Attribute("id", String)
			Attribute("children", ArrayOf(plainChild))
			Attribute("sidecar", plainChild)
			Required("id")
		})
		defaults := Type("Defaults", func() {
			Attribute("id", String, func() { Meta("struct:field:type", "uuid.UUID", "github.com/google/uuid"); Default("invalid") })
			Attribute("children", ArrayOf(defaultChild))
		})
		Service("example", func() {
			Method("parse", func() { Payload(custom); Result(plain) })
			Agent("assistant", "Assistant", func() {
				Use("query", func() {
					Tool("parse", "Parse", func() {
						Args(plain)
						Return(custom)
						BindTo("example", "parse")
						ServerData("identity", custom, func() { FromMethodResultField("sidecar") })
					})
					Tool("defaults", "Defaults", func() {
						Args(defaults)
						Return(String)
					})
				})
			})
		})
	})
	dir := writeGeneratedModule(t, files)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fallible_test.go"), []byte(fallibleRuntimeTest), 0600))
	command := exec.CommandContext(t.Context(), "go", "test", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "generated conversions: %s", output)
}

const fallibleRuntimeTest = `package fmcp_test
import (
 "context"
 "github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
 "testing"
 service "example.com/fmcp/gen/example"
 query "example.com/fmcp/gen/example/toolsets/query"
 "github.com/stretchr/testify/require"
)
func TestConversions(t *testing.T) {
 valid := "550e8400-e29b-41d4-a716-446655440000"
 input,err := query.InitParseMethodPayload(&query.ParsePayload{ID: valid})
 require.NoError(t,err)
 require.Equal(t,valid,input.ID.String())
 input,err = query.InitParseMethodPayload(&query.ParsePayload{ID: "invalid"})
 require.Error(t,err)
 require.Nil(t,input)
 output,err := query.InitParseToolResult(&service.Plain{ID: valid})
 require.NoError(t,err)
 require.Equal(t,valid,output.ID.String())
 output,err = query.InitParseToolResult(&service.Plain{ID: "invalid"})
 require.Error(t,err)
 require.Nil(t,output)
 defaults,err := query.DefaultsPayloadCodec.FromJSON([]byte("{}"))
 require.Error(t,err)
 require.Nil(t,defaults)
 defaults,err = query.DefaultsPayloadCodec.FromJSON([]byte(` + "`" + `{"id":"550e8400-e29b-41d4-a716-446655440000","children":[{}]}` + "`" + `))
 require.Error(t,err)
 require.Nil(t,defaults)
 sidecar,err := query.InitParseIdentityServerData(&service.PlainChild{ID:"invalid"})
 require.Error(t,err)
 require.Nil(t,sidecar)
 sidecar,err = query.InitParseIdentityServerData(nil)
 require.NoError(t,err)
 require.Nil(t,sidecar)
 output,err = query.InitParseToolResult(&service.Plain{ID:valid,Children:[]*service.PlainChild{{ID:"invalid"}}})
 require.Error(t,err)
 require.Nil(t,output)
}
func TestProviderAndDispatcherStopAtFailedConversions(t *testing.T) {
 valid := "550e8400-e29b-41d4-a716-446655440000"
 for _, tc := range []struct{name,input,output,sidecar,code string; calls int}{
  {"payload", "private-invalid", valid, "", "invalid_arguments", 0},
  {"result", valid, "private-invalid", "", "encode_failed", 1},
  {"server data", valid, valid, "private-invalid", "encode_failed", 1},
  {"valid", valid, valid, valid, "", 1},
 } {
  t.Run(tc.name,func(t *testing.T){
   svc := &conversionService{output:&service.Plain{ID:tc.output}}
   if tc.sidecar!="" { svc.output.Sidecar=&service.PlainChild{ID:tc.sidecar} }
   raw:=[]byte("{\"id\":\""+tc.input+"\"}")
   provider:=query.NewProvider(svc)
   ctx:=toolregistry.WithToolUseID(context.Background(),"use-1")
   result,err:=provider.HandleToolCall(ctx,toolregistry.ToolCallMessage{RegistrationToken:"registration",ToolUseID:"use-1",Tool:query.Parse,Payload:raw,Meta:&toolregistry.ToolCallMeta{}})
   require.NoError(t,err)
   require.Equal(t,tc.calls,svc.calls)
   if tc.code=="" { require.Nil(t,result.Error) } else {
    require.NotNil(t,result.Error)
    require.Equal(t,tc.code,result.Error.Code)
    require.NotContains(t,result.Error.Message,"private-invalid")
   }
   calls:=0
   dispatched,err:=query.DispatchParseMethod(context.Background(),nil,raw,nil,query.ParseDispatchOptions{Call:func(_ context.Context,_ any)(any,error){ calls++; return svc.output,nil }})
   require.NoError(t,err)
   require.Equal(t,tc.calls,calls)
   if tc.code=="" { require.Nil(t,dispatched.Error) } else { require.NotNil(t,dispatched.Error); require.Nil(t,dispatched.Result) }
  })
 }
}
type conversionService struct{output *service.Plain;calls int}
func (s *conversionService) Parse(context.Context,*service.Custom)(*service.Plain,error){ s.calls++;return s.output,nil }

`
