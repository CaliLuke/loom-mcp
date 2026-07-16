package codegen

import (
	"bytes"
	"testing"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/stretchr/testify/require"
)

func TestMCPJSONRPCBatchHandlerSectionOwnsCompleteHandler(t *testing.T) {
	data := testMCPJSONRPCBatchServiceData()
	var rendered bytes.Buffer
	section := mcpJSONRPCBatchHandlerSection(data)

	err := section.Write(&rendered)

	require.NoError(t, err)
	require.IsType(t, &codegen.JenniferSection{}, section)
	source := rendered.String()
	require.Contains(t, source, "func (s *Server) handleHTTP(")
	require.Contains(t, source, "func (s *Server) handleSingle(")
	require.Contains(t, source, "func (s *Server) handleBatch(")
	require.Contains(t, source, "func (s *Server) processRequest(")
	require.Contains(t, source, `case "ping":`)
	require.Contains(t, source, `case "tools/call":`)
	require.Contains(t, source, "loomtransport.ReasonInvalidJSONRPCBatch")
	require.Contains(t, source, "loomtransport.ReasonUnsupportedMethod")
	require.Contains(t, source, "s.processBatchRequest(r.Context(), r, &req, writer)")
	require.NotContains(t, source, "s.processRequest(r.Context(), r, &req, writer)")
	require.Contains(t, source, "type mcpBatchResponseWriter struct")
	require.Contains(t, source, "func (w *mcpBatchResponseWriter) Flush()")
	require.Contains(t, source, `strings.Contains(buffer.Header().Get("Content-Type"), "text/event-stream")`)
	require.Contains(t, source, "func finalMCPBatchSSEResponse(body []byte) []byte")
	require.Contains(t, source, "streaming method did not produce a final response")
	require.NotContains(t, source, "func (s *Server) serveHTTP(", "the mixed transport section owns serveHTTP")
}

func TestApplyMCPJSONRPCBatchHandlerSectionRequiresExactOwner(t *testing.T) {
	data := testMCPJSONRPCBatchServiceData()
	path := "gen/jsonrpc/mcp_assistant/server/server.go"
	tests := []struct {
		name  string
		files []*codegen.File
		want  string
	}{
		{
			name:  "missing",
			files: []*codegen.File{{Path: path, Sections: []codegen.Section{codegen.NewRawSection("changed-handler", "")}}},
			want:  `expected one "jsonrpc-server-handler" section, found 0`,
		},
		{
			name: "duplicate",
			files: []*codegen.File{{Path: path, Sections: []codegen.Section{
				codegen.NewRawSection(jsonrpcServerHandlerSectionName, ""),
				codegen.NewRawSection(jsonrpcServerHandlerSectionName, ""),
			}}},
			want: `expected one "jsonrpc-server-handler" section, found 2`,
		},
		{
			name: "multiple files",
			files: []*codegen.File{
				{Path: path, Sections: []codegen.Section{codegen.NewRawSection(jsonrpcServerHandlerSectionName, "")}},
				{Path: "other/gen/jsonrpc/mcp_other/server/server.go", Sections: []codegen.Section{codegen.NewRawSection(jsonrpcServerHandlerSectionName, "")}},
			},
			want: `expected one "jsonrpc-server-handler" section, found 2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := applyMCPJSONRPCBatchHandlerSection(tt.files, data)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestApplyMCPJSONRPCBatchHandlerSectionReplacesNamedSection(t *testing.T) {
	data := testMCPJSONRPCBatchServiceData()
	file := &codegen.File{
		Path: "gen/jsonrpc/mcp_assistant/server/server.go",
		Sections: []codegen.Section{
			codegen.NewRawSection("before", "const before = true"),
			codegen.NewRawSection(jsonrpcServerHandlerSectionName, "const legacy = true"),
			codegen.NewRawSection("after", "const after = true"),
		},
	}

	err := applyMCPJSONRPCBatchHandlerSection([]*codegen.File{file}, data)

	require.NoError(t, err)
	require.Len(t, file.AllSections(), 3)
	rendered := renderMCPJSONRPCBatchOwnerFile(t, file)
	require.Contains(t, rendered, "const before = true")
	require.Contains(t, rendered, "func (s *Server) processBatchRequest(")
	require.Contains(t, rendered, "const after = true")
	require.NotContains(t, rendered, "const legacy = true")
}

func TestApplyMCPJSONRPCBatchHandlerSectionRejectsNilServiceData(t *testing.T) {
	err := applyMCPJSONRPCBatchHandlerSection(nil, nil)

	require.ErrorContains(t, err, "requires service data")
}

func testMCPJSONRPCBatchServiceData() *httpcodegen.ServiceData {
	return &httpcodegen.ServiceData{
		Service:      &service.Data{Name: "mcp_assistant"},
		ServerStruct: "Server",
		Endpoints: []*httpcodegen.EndpointData{
			{Method: &service.MethodData{Name: "ping", VarName: "Ping"}},
			{Method: &service.MethodData{Name: "tools/call", VarName: "ToolsCall"}, SSE: &httpcodegen.SSEData{}},
		},
	}
}

func renderMCPJSONRPCBatchOwnerFile(t *testing.T, file *codegen.File) string {
	t.Helper()
	var rendered bytes.Buffer
	for _, section := range file.AllSections() {
		require.NoError(t, section.Write(&rendered))
	}
	return rendered.String()
}
