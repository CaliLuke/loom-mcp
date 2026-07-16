package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/stretchr/testify/require"
)

func TestMCPJSONRPCHandlerInitSectionOwnsMCPBehavior(t *testing.T) {
	section := mcpJSONRPCHandlerInitSection(testMCPJSONRPCEndpoint("ping", "Ping"))

	_, ok := section.(*codegen.JenniferSection)
	require.True(t, ok)

	var source bytes.Buffer
	require.NoError(t, section.Write(&source))
	generated := source.String()
	require.Contains(t, generated, "func NewPingHandler(")
	require.Contains(t, generated, "jsonrpc.MakeSuccessResponse(req.ID, struct{}{})")
	require.Contains(t, generated, `case "resource_not_found":`)
	require.Contains(t, generated, "jsonrpc.Code(-32002)")
	require.Contains(t, generated, "mcpruntime.NewErrorData(err)")
	require.NotContains(t, generated, "jsonrpc.NewErrorData(err)")
}

func TestMCPJSONRPCHandlerInitSectionReusesPayloadDecodeError(t *testing.T) {
	endpoint := testMCPJSONRPCEndpoint("notifications/initialized", "NotificationsInitialized")
	endpoint.Payload = &httpcodegen.PayloadData{Ref: "*mcp.NotificationsInitializedPayload"}
	endpoint.RequestDecoder = "DecodeNotificationsInitializedRequest"
	section := mcpJSONRPCHandlerInitSection(endpoint)

	var source bytes.Buffer
	require.NoError(t, section.Write(&source))
	generated := source.String()
	require.Contains(t, generated, "params, err := decodeParams(r, req)")
	require.Contains(t, generated, "_, err = endpoint(ctx, params)")
	require.NotContains(t, generated, "_, err := endpoint(ctx, params)")
}

func TestMCPJSONRPCHandlerInitSectionOwnsSSEErrors(t *testing.T) {
	endpoint := testMCPJSONRPCEndpoint("events/stream", "EventsStream")
	endpoint.Method.ServerStream = &service.StreamData{EndpointStruct: "EventsStreamEndpointInput"}
	endpoint.SSE = &httpcodegen.SSEData{StructName: "EventsStreamServerStream"}
	section := mcpJSONRPCHandlerInitSection(endpoint)

	var source bytes.Buffer
	require.NoError(t, section.Write(&source))
	generated := source.String()
	require.Contains(t, generated, "streamWritePolicy loomhttp.StreamWritePolicy")
	require.Contains(t, generated, "strm := &EventsStreamServerStream{")
	require.Contains(t, generated, `case "resource_not_found":`)
	require.Contains(t, generated, "jsonrpc.Code(-32002)")
	require.Contains(t, generated, "mcpruntime.NewErrorData(err)")
	require.NotContains(t, generated, "jsonrpc.NewErrorData(err)")
}

func TestReplaceMCPJSONRPCHandlerInitSectionsUsesEndpointOrder(t *testing.T) {
	data := &httpcodegen.ServiceData{Endpoints: []*httpcodegen.EndpointData{
		testMCPJSONRPCEndpoint("ping", "Ping"),
		testMCPJSONRPCEndpoint("tools/list", "ToolsList"),
	}}
	file := testMCPJSONRPCServerFile(
		codegen.NewRawSection("before", "var before bool\n"),
		codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "upstream ping"),
		codegen.NewRawSection("middle", "var middle bool\n"),
		codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "upstream tools"),
	)

	require.NoError(t, replaceMCPJSONRPCHandlerInitSections([]*codegen.File{file}, data))
	sections := file.AllSections()
	require.Len(t, sections, 4)
	require.Equal(t, "before", sections[0].SectionName())
	if got := sections[1].SectionName(); got != jsonrpcServerHandlerInitSectionName {
		t.Fatalf("first replaced section name = %q, want %q", got, jsonrpcServerHandlerInitSectionName)
	}
	require.Equal(t, "middle", sections[2].SectionName())
	if got := sections[3].SectionName(); got != jsonrpcServerHandlerInitSectionName {
		t.Fatalf("second replaced section name = %q, want %q", got, jsonrpcServerHandlerInitSectionName)
	}

	var first bytes.Buffer
	var second bytes.Buffer
	require.NoError(t, sections[1].Write(&first))
	require.NoError(t, sections[3].Write(&second))
	require.Contains(t, first.String(), "func NewPingHandler(")
	require.Contains(t, second.String(), "func NewToolsListHandler(")
}

func TestReplaceMCPJSONRPCHandlerInitSectionsRejectsDrift(t *testing.T) {
	data := &httpcodegen.ServiceData{Endpoints: []*httpcodegen.EndpointData{
		testMCPJSONRPCEndpoint("ping", "Ping"),
		testMCPJSONRPCEndpoint("tools/list", "ToolsList"),
	}}

	tests := []struct {
		name  string
		files []*codegen.File
		want  string
	}{
		{
			name: "missing section",
			files: []*codegen.File{testMCPJSONRPCServerFile(
				codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "one"),
			)},
			want: `expected 2 "jsonrpc-server-handler-init" sections, found 1`,
		},
		{
			name: "duplicate section",
			files: []*codegen.File{testMCPJSONRPCServerFile(
				codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "one"),
				codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "two"),
				codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "three"),
			)},
			want: `expected 2 "jsonrpc-server-handler-init" sections, found 3`,
		},
		{
			name:  "missing server file",
			files: []*codegen.File{{Path: "gen/jsonrpc/mcp/client/client.go"}},
			want:  "expected one server.go file, found 0",
		},
		{
			name: "duplicate server file",
			files: []*codegen.File{
				testMCPJSONRPCServerFile(
					codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "one"),
					codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "two"),
				),
				testMCPJSONRPCServerFile(
					codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "one"),
					codegen.NewRawSection(jsonrpcServerHandlerInitSectionName, "two"),
				),
			},
			want: "expected one server.go file, found 2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := replaceMCPJSONRPCHandlerInitSections(test.files, data)
			require.Error(t, err)
			require.Contains(t, err.Error(), test.want)
		})
	}
}

func testMCPJSONRPCEndpoint(name, goName string) *httpcodegen.EndpointData {
	return &httpcodegen.EndpointData{
		Method: &service.MethodData{
			Name:    name,
			VarName: goName,
		},
		ServiceName:    "mcp",
		ServicePkgName: "mcp",
		HandlerInit:    "New" + goName + "Handler",
	}
}

func testMCPJSONRPCServerFile(sections ...codegen.Section) *codegen.File {
	return &codegen.File{
		Path:     strings.Join([]string{"gen", "jsonrpc", "mcp", "server", "server.go"}, "/"),
		Sections: sections,
	}
}
