package planner

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestToolResultArtifactsUseContentAndToolOutputUsesRefs(t *testing.T) {
	content := artifact.Content{
		Ref: artifact.Ref{
			ID:         "artifact-1",
			AgentID:    "svc.agent",
			RunID:      "run-1",
			ToolCallID: "call-1",
			Name:       "report.txt",
			MimeType:   "text/plain",
			SizeBytes:  11,
			Metadata:   map[string]string{"kind": "report"},
		},
		Body: []byte("hello world"),
	}

	result := &ToolResult{
		Name:       tools.Ident("svc.tools.export"),
		ToolCallID: "call-1",
		Result:     map[string]string{"status": "ok"},
		Artifacts:  []artifact.Content{content},
	}
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, []byte("hello world"), result.Artifacts[0].Body)

	output := &ToolOutput{
		Name:       result.Name,
		ToolCallID: result.ToolCallID,
		Payload:    rawjson.Message([]byte(`{"format":"txt"}`)),
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		Artifacts:  []artifact.Ref{content.Ref},
	}
	require.Len(t, output.Artifacts, 1)
	require.Equal(t, "artifact-1", output.Artifacts[0].ID)
}
