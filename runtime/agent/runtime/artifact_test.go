package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
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

func TestArtifactToolsetStoreUnavailableReturnsStructuredToolError(t *testing.T) {
	reg := NewArtifactToolsetRegistration(ArtifactToolsetConfig{Name: "artifacts"})
	result, err := reg.Execute(context.Background(), &planner.ToolRequest{
		Name:       "artifacts.list_artifacts",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "artifact-list",
		Payload:    rawjson.Message([]byte(`{"limit":5}`)),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ToolResult.Error)
	require.NotNil(t, result.ToolResult.RetryHint)
	require.Equal(t, planner.RetryReasonUnsupportedOperation, result.ToolResult.RetryHint.Reason)
	require.Contains(t, result.ToolResult.RetryHint.Message, "runtime.WithArtifactStore")
}

func TestArtifactToolsetWithoutStoreDoesNotAdvertiseArtifactTools(t *testing.T) {
	rt := New()
	reg := NewArtifactToolsetRegistration(ArtifactToolsetConfig{Name: "artifacts"})
	require.Empty(t, reg.Specs)

	err := rt.RegisterToolset(reg)
	require.NoError(t, err)
	_, ok := rt.ToolSpec("artifacts.list_artifacts")
	require.False(t, ok)
}

func TestArtifactToolsetLoadAppliesDefaultConfiguredAndUnlimitedLimits(t *testing.T) {
	store := artifact.NewMemoryStore()
	body := strings.Repeat("x", DefaultArtifactLoadMaxBytes+1)
	ref, err := store.Save(context.Background(), artifact.SaveInput{
		AgentID:  "svc.agent",
		RunID:    "run-1",
		Name:     "large.txt",
		MimeType: "text/plain",
		Body:     []byte(body),
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		configMax int
		payload   string
		wantBytes int
		wantTrunc bool
	}{
		{
			name:      "default cap",
			payload:   `{"id":"` + ref.ID + `"}`,
			wantBytes: DefaultArtifactLoadMaxBytes,
			wantTrunc: true,
		},
		{
			name:      "configured cap overrides default",
			configMax: 5,
			payload:   `{"id":"` + ref.ID + `","max_bytes":20}`,
			wantBytes: 5,
			wantTrunc: true,
		},
		{
			name:      "explicit unlimited",
			configMax: UnlimitedToolsetLimit,
			payload:   `{"id":"` + ref.ID + `"}`,
			wantBytes: DefaultArtifactLoadMaxBytes + 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewArtifactToolsetRegistration(ArtifactToolsetConfig{
				Store:            store,
				Name:             "artifacts",
				MaxArtifactBytes: tt.configMax,
			})
			result, err := reg.Execute(context.Background(), &planner.ToolRequest{
				Name:       "artifacts.load_artifact",
				AgentID:    "svc.agent",
				RunID:      "run-1",
				ToolCallID: "artifact-load",
				Payload:    rawjson.Message([]byte(tt.payload)),
			})
			require.NoError(t, err)
			require.Nil(t, result.ToolResult.Error)

			got, ok := result.ToolResult.Result.(artifactLoadResult)
			require.True(t, ok)
			require.Len(t, got.Content, tt.wantBytes)
			require.Equal(t, tt.wantTrunc, got.Truncated)
			require.Equal(t, int64(DefaultArtifactLoadMaxBytes+1), got.SizeBytes)
		})
	}
}

func TestArtifactToolsetListAppliesDefaultConfiguredAndUnlimitedLimits(t *testing.T) {
	store := artifact.NewMemoryStore()
	for range DefaultArtifactListLimit + 1 {
		_, err := store.Save(context.Background(), artifact.SaveInput{
			AgentID: "svc.agent",
			RunID:   "run-1",
			Body:    []byte("body"),
		})
		require.NoError(t, err)
	}

	tests := []struct {
		name      string
		configMax int
		payload   string
		wantCount int
	}{
		{
			name:      "default cap",
			payload:   `{}`,
			wantCount: DefaultArtifactListLimit,
		},
		{
			name:      "configured cap overrides default",
			configMax: 2,
			payload:   `{"limit":20}`,
			wantCount: 2,
		},
		{
			name:      "explicit unlimited",
			configMax: UnlimitedToolsetLimit,
			payload:   `{}`,
			wantCount: DefaultArtifactListLimit + 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewArtifactToolsetRegistration(ArtifactToolsetConfig{
				Store:        store,
				Name:         "artifacts",
				MaxArtifacts: tt.configMax,
			})
			result, err := reg.Execute(context.Background(), &planner.ToolRequest{
				Name:       "artifacts.list_artifacts",
				AgentID:    "svc.agent",
				RunID:      "run-1",
				ToolCallID: "artifact-list",
				Payload:    rawjson.Message([]byte(tt.payload)),
			})
			require.NoError(t, err)
			require.Nil(t, result.ToolResult.Error)

			got, ok := result.ToolResult.Result.(artifactListResult)
			require.True(t, ok)
			require.Len(t, got.Artifacts, tt.wantCount)
		})
	}
}

func TestProvidedToolResultArtifactRefsRejectForeignScope(t *testing.T) {
	rt := &Runtime{
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			"svc.ts.export": newAnyJSONSpec("svc.ts.export", "svc.ts"),
		},
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}
	call := planner.ToolRequest{
		Name:       "svc.ts.export",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
	}

	_, _, err := rt.decodeProvidedToolResult(context.Background(), rt.toolSpecs[call.Name], call, &api.ProvidedToolResult{
		ToolCallID: "call-1",
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		Artifacts: []artifact.Ref{{
			ID:      "artifact-1",
			AgentID: "other.agent",
			RunID:   "other-run",
		}},
	})
	require.ErrorContains(t, err, "artifact ref scope")
}

func TestArtifactRefsRejectMismatchedToolCallID(t *testing.T) {
	rt := &Runtime{
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			"svc.ts.export": newAnyJSONSpec("svc.ts.export", "svc.ts"),
		},
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}
	call := planner.ToolRequest{
		Name:       "svc.ts.export",
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
	}

	_, _, err := rt.decodeProvidedToolResult(context.Background(), rt.toolSpecs[call.Name], call, &api.ProvidedToolResult{
		ToolCallID: "call-1",
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		Artifacts: []artifact.Ref{{
			ID:         "artifact-1",
			AgentID:    "svc.agent",
			RunID:      "run-1",
			ToolCallID: "other-call",
		}},
	})
	require.ErrorContains(t, err, "artifact ref scope")
}
