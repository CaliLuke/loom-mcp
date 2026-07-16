package codegen

import (
	"testing"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/stretchr/testify/require"
)

func TestOwnMCPJSONRPCSSEStreamSectionsReplacesExactEndpointSections(t *testing.T) {
	data := &httpcodegen.ServiceData{Endpoints: []*httpcodegen.EndpointData{
		{Method: &service.MethodData{Name: "ping", VarName: "Ping"}},
		{Method: &service.MethodData{Name: "tools/call", VarName: "ToolsCall"}, SSE: &httpcodegen.SSEData{}},
		{Method: &service.MethodData{Name: "events/stream", VarName: "EventsStream"}, SSE: &httpcodegen.SSEData{}},
	}}
	file := &codegen.File{
		Path: "gen/jsonrpc/mcp_assistant/server/stream.go",
		Sections: []codegen.Section{
			codegen.NewRawSection("before", "before"),
			codegen.NewRawSection(jsonrpcSSEServerStreamSectionName, "tools"),
			codegen.NewRawSection(jsonrpcSSEServerStreamSectionName, "events"),
		},
	}

	require.NoError(t, ownMCPJSONRPCSSEStreamSections([]*codegen.File{file}, data))
	sections := file.AllSections()
	require.Len(t, sections, 3)
	require.IsType(t, &codegen.JenniferSection{}, sections[1])
	require.IsType(t, &codegen.JenniferSection{}, sections[2])
}

func TestOwnMCPJSONRPCSSEStreamSectionsRejectsDrift(t *testing.T) {
	data := &httpcodegen.ServiceData{Endpoints: []*httpcodegen.EndpointData{{
		Method: &service.MethodData{Name: "events/stream", VarName: "EventsStream"},
		SSE:    &httpcodegen.SSEData{},
	}}}
	tests := []struct {
		name  string
		files []*codegen.File
		want  string
	}{
		{
			name:  "missing file",
			files: []*codegen.File{{Path: "gen/jsonrpc/mcp_assistant/server/server.go"}},
			want:  "expected one stream.go",
		},
		{
			name: "missing section",
			files: []*codegen.File{{
				Path:     "gen/jsonrpc/mcp_assistant/server/stream.go",
				Sections: []codegen.Section{codegen.NewRawSection("changed", "changed")},
			}},
			want: `expected one stream.go and 1 "jsonrpc-sse-server-stream" sections`,
		},
		{
			name: "duplicate section",
			files: []*codegen.File{{
				Path: "gen/jsonrpc/mcp_assistant/server/stream.go",
				Sections: []codegen.Section{
					codegen.NewRawSection(jsonrpcSSEServerStreamSectionName, "one"),
					codegen.NewRawSection(jsonrpcSSEServerStreamSectionName, "two"),
				},
			}},
			want: `expected 1 "jsonrpc-sse-server-stream" sections, found more`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ownMCPJSONRPCSSEStreamSections(tt.files, data)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
