package hooks

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestToolResultReceivedArtifactsRoundTripThroughHookCodec(t *testing.T) {
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

	ev := NewToolResultReceivedEvent(
		testRunID,
		agent.Ident("svc.agent"),
		testSessionID,
		tools.Ident("svc.tools.export"),
		"call-1",
		"",
		nil,
		rawjson.Message([]byte(`{"status":"ok"}`)),
		nil,
		"preview",
		nil,
		250*time.Millisecond,
		nil,
		nil,
		nil,
	)
	ev.Artifacts = []artifact.Ref{ref}

	in, err := EncodeToHookInput(ev, "")
	require.NoError(t, err)
	require.Contains(t, string(in.Payload), `"artifacts"`)
	require.NotContains(t, string(in.Payload), "hello world")

	decoded, err := DecodeFromHookInput(in)
	require.NoError(t, err)
	got, ok := decoded.(*ToolResultReceivedEvent)
	require.True(t, ok)
	require.Len(t, got.Artifacts, 1)
	require.Equal(t, ref.ID, got.Artifacts[0].ID)
	require.Equal(t, ref.Metadata, got.Artifacts[0].Metadata)
}
