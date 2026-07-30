package debug

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/require"
)

func TestServerEndpointsReturnDataEnvelopes(t *testing.T) {
	t.Parallel()

	rt := seededRuntime(t)
	srv, err := NewServer(Config{Runtime: rt})
	require.NoError(t, err)

	for _, path := range []string{
		"/runs/run-1",
		"/runs/run-1/events",
		"/runs/run-1/await",
		"/runs/run-1/memory",
		"/runs/run-1/artifacts",
		"/runs/run-1/workflow",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Contains(t, rec.Body.String(), `"data"`, path)
	}
}

func TestServerErrorsUseErrorEnvelope(t *testing.T) {
	t.Parallel()

	srv, err := NewServer(Config{Runtime: agentsruntime.New()})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/runs/missing", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), `"error"`)
	require.Contains(t, rec.Body.String(), `"not_found"`)
}

func TestWorkflowEndpointIncludesDerivedGraphState(t *testing.T) {
	t.Parallel()

	rt := seededRuntime(t)
	srv, err := NewServer(Config{Runtime: rt})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/runs/run-1/workflow", nil)
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `"nodes"`)
	require.Contains(t, body, `"draft"`)
	require.Contains(t, body, `"status":"completed"`)
	require.Contains(t, body, `"awaits"`)
	require.Contains(t, body, `"approval"`)
}

func TestServerStartDefaultsToLoopbackAndShutdown(t *testing.T) {
	t.Parallel()

	srv, err := NewServer(Config{Runtime: agentsruntime.New()})
	require.NoError(t, err)
	require.Equal(t, defaultAddr, srv.Addr())

	require.NoError(t, srv.Start())
	require.True(t, strings.HasPrefix(srv.Addr(), "127.0.0.1:"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
}

func TestServerStartUsesExplicitBindAddress(t *testing.T) {
	t.Parallel()

	srv, err := NewServer(Config{Runtime: agentsruntime.New(), Addr: "127.0.0.1:0"})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	require.True(t, strings.HasPrefix(srv.Addr(), "127.0.0.1:"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
}

func TestServerConstructionDoesNotChangeRuntimeHookBehavior(t *testing.T) {
	t.Parallel()

	rt := agentsruntime.New()
	_, err := NewServer(Config{Runtime: rt})
	require.NoError(t, err)

	_, err = rt.RunEventStore.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-1",
		RunID:     "run-1",
		AgentID:   agent.Ident("svc.agent"),
		Type:      hooks.PlannerNote,
		Payload:   rawjson.Message(`{"note":"ok"}`),
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)
	page, err := rt.ListRunEvents(context.Background(), "run-1", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
}

func seededRuntime(t *testing.T) *agentsruntime.Runtime {
	t.Helper()

	sessionStore := sessioninmem.New()
	runlogStore := runloginmem.New()
	memoryStore := memoryinmem.New()
	artifactStore := artifact.NewMemoryStore()
	rt := agentsruntime.New(
		agentsruntime.WithSessionStore(sessionStore),
		agentsruntime.WithRunEventStore(runlogStore),
		agentsruntime.WithMemoryStore(memoryStore),
		agentsruntime.WithArtifactStore(artifactStore),
	)
	now := time.Now().UTC()
	_, err := sessionStore.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, sessionStore.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))
	_, err = runlogStore.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-1",
		RunID:     "run-1",
		AgentID:   "svc.agent",
		Type:      hooks.ToolCallScheduled,
		Payload:   rawjson.Message(`{"tool_call_id":"draft","tool_name":"writer.draft","queue":"default"}`),
		Timestamp: now,
	})
	require.NoError(t, err)
	_, err = runlogStore.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-2",
		RunID:     "run-1",
		AgentID:   "svc.agent",
		Type:      hooks.ToolResultReceived,
		Payload:   rawjson.Message(`{"tool_call_id":"draft","tool_name":"writer.draft"}`),
		Timestamp: now,
	})
	require.NoError(t, err)
	_, err = runlogStore.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-3",
		RunID:     "run-1",
		AgentID:   "svc.agent",
		Type:      hooks.AwaitTypedInput,
		Payload:   rawjson.Message(`{"id":"approval"}`),
		Timestamp: now,
	})
	require.NoError(t, err)
	require.NoError(t, memoryStore.AppendEvents(context.Background(), "svc.agent", "run-1", memory.Event{
		Type:      memory.EventUserMessage,
		Timestamp: now,
		Data:      map[string]any{"text": "hello"},
	}))
	_, err = artifactStore.Save(context.Background(), artifact.SaveInput{
		AgentID:  "svc.agent",
		RunID:    "run-1",
		Name:     "note.txt",
		MimeType: "text/plain",
		Body:     []byte("hello"),
	})
	require.NoError(t, err)
	return rt
}
