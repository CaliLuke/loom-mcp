package api

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestToolWorkflowEnvelopesCarryArtifactRefs(t *testing.T) {
	ref := artifact.Ref{
		ID:         "artifact-1",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
		Name:       "report.txt",
		MimeType:   "text/plain",
		SizeBytes:  11,
		Metadata:   map[string]string{"kind": "report"},
	}

	event := ToolEvent{
		Name:       tools.Ident("svc.tools.export"),
		ToolCallID: "call-1",
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		Artifacts:  []artifact.Ref{ref},
	}
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"artifacts"`)
	require.NotContains(t, string(encoded), "hello world")

	var decoded ToolEvent
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Len(t, decoded.Artifacts, 1)
	require.Equal(t, ref.ID, decoded.Artifacts[0].ID)

	output := ToolCallOutput{
		Name:       event.Name,
		ToolCallID: event.ToolCallID,
		Result:     event.Result,
		Artifacts:  []artifact.Ref{ref},
	}
	encoded, err = json.Marshal(output)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hello world")

	var decodedOutput ToolCallOutput
	require.NoError(t, json.Unmarshal(encoded, &decodedOutput))
	require.Equal(t, ref.ID, decodedOutput.Artifacts[0].ID)
}

func TestProvidedToolResultRejectsArtifactBodiesAtBoundary(t *testing.T) {
	ref := artifact.Ref{
		ID:         "artifact-1",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
		Name:       "report.txt",
		MimeType:   "text/plain",
		SizeBytes:  11,
	}

	provided := ProvidedToolResult{
		Name:       tools.Ident("svc.tools.export"),
		ToolCallID: "call-1",
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		Artifacts:  []artifact.Ref{ref},
	}
	encoded, err := json.Marshal(provided)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hello world")
}
