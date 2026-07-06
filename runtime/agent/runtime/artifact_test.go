package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestBuildPlannerToolOutputsPersistsArtifactContentAsRefs(t *testing.T) {
	store := artifact.NewMemoryStore()
	rt := &Runtime{
		ArtifactStore: store,
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			"svc.ts.export": newAnyJSONSpec("svc.ts.export", "svc.ts"),
		},
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}

	outputs, err := rt.buildPlannerToolOutputs(
		context.Background(),
		[]planner.ToolRequest{{
			Name:       "svc.ts.export",
			AgentID:    "svc.agent",
			RunID:      "run-1",
			ToolCallID: "call-1",
			Payload:    rawjson.Message([]byte(`{"format":"txt"}`)),
		}},
		[]*planner.ToolResult{{
			Name:       "svc.ts.export",
			ToolCallID: "call-1",
			Result:     map[string]string{"status": "ok"},
			Artifacts: []artifact.Content{{
				Ref: artifact.Ref{
					Name:     "report.txt",
					MimeType: "text/plain",
					Metadata: map[string]string{"kind": "report"},
				},
				Body: []byte("hello world"),
			}},
		}},
	)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].Artifacts, 1)
	require.NotEmpty(t, outputs[0].Artifacts[0].ID)
	require.Equal(t, "svc.agent", outputs[0].Artifacts[0].AgentID)
	require.Equal(t, "run-1", outputs[0].Artifacts[0].RunID)
	require.Equal(t, "call-1", outputs[0].Artifacts[0].ToolCallID)

	loaded, err := store.Load(context.Background(), artifact.LoadQuery{
		AgentID: "svc.agent",
		RunID:   "run-1",
		ID:      outputs[0].Artifacts[0].ID,
	})
	require.NoError(t, err)
	require.Equal(t, []byte("hello world"), loaded.Body)
}

func TestArtifactToolsetListsAndLoadsArtifacts(t *testing.T) {
	store := artifact.NewMemoryStore()
	rt := New(WithArtifactStore(store))
	reg := NewArtifactToolsetRegistration(ArtifactToolsetConfig{
		Store:            store,
		Name:             "artifacts",
		MaxArtifactBytes: 65536,
		MaxArtifacts:     50,
	})
	require.NoError(t, rt.RegisterToolset(reg))

	ref, err := store.Save(context.Background(), artifact.SaveInput{
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
		Name:       "report.txt",
		MimeType:   "text/plain",
		Body:       []byte("hello world"),
	})
	require.NoError(t, err)

	listResult, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "artifacts.list_artifacts",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "artifact-list",
		Payload:    rawjson.Message([]byte(`{"mime_type":"text/plain","limit":50}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, listResult.ToolResult)
	listBytes, err := json.Marshal(listResult.ToolResult.Result)
	require.NoError(t, err)
	require.Contains(t, string(listBytes), ref.ID)
	require.NotContains(t, string(listBytes), "hello world")

	loadResult, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "artifacts.load_artifact",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "artifact-load",
		Payload:    rawjson.Message([]byte(`{"id":"` + ref.ID + `","max_bytes":5}`)),
	})
	require.NoError(t, err)
	loadBytes, err := json.Marshal(loadResult.ToolResult.Result)
	require.NoError(t, err)
	require.JSONEq(t, `{"content":"hello","mime_type":"text/plain","truncated":true,"size_bytes":11}`, string(loadBytes))
}
