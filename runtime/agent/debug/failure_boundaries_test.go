package debug

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedDebugMemory struct {
	memory.Store
}

type failedDebugArtifacts struct {
	artifact.Store
	err error
}

type failedDebugEvents struct {
	runlog.Store
}

func TestDebugServerReportsUnavailableAndFailedStores(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		setup  func(*agentsruntime.Runtime)
		status int
		code   string
	}{
		{"memory absent", "/runs/run-1/memory", func(rt *agentsruntime.Runtime) {
			rt.Memory = nil
		}, 503, "unavailable"},
		{"artifacts absent", "/runs/run-1/artifacts", func(rt *agentsruntime.Runtime) {
			rt.ArtifactStore = nil
		}, 503, "unavailable"},
		{"memory failure", "/runs/run-1/memory", func(rt *agentsruntime.Runtime) {
			rt.Memory = failedDebugMemory{}
		}, 500, "internal"},
		{"artifact failure", "/runs/run-1/artifacts", func(rt *agentsruntime.Runtime) {
			rt.ArtifactStore = failedDebugArtifacts{err: assert.AnError}
		}, 500, "internal"},
		{"artifact missing", "/runs/run-1/artifacts", func(rt *agentsruntime.Runtime) {
			rt.ArtifactStore = failedDebugArtifacts{err: artifact.ErrNotFound}
		}, 404, "not_found"},
		{"events failure", "/runs/run-1/events", func(rt *agentsruntime.Runtime) {
			rt.RunEventStore = failedDebugEvents{}
		}, 500, "internal"},
		{"await failure", "/runs/run-1/await", func(rt *agentsruntime.Runtime) {
			rt.RunEventStore = failedDebugEvents{}
		}, 500, "internal"},
		{"workflow failure", "/runs/run-1/workflow", func(rt *agentsruntime.Runtime) {
			rt.RunEventStore = failedDebugEvents{}
		}, 500, "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := seededRuntime(t)
			tc.setup(rt)
			server, err := NewServer(Config{Runtime: rt})
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))
			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, rec.Body.String(), `"code":"`+tc.code+`"`)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		})
	}
}

func TestDebugServerRejectsUnknownPathsAndMissingRuns(t *testing.T) {
	server, err := NewServer(Config{Runtime: seededRuntime(t)})
	require.NoError(t, err)
	for _, path := range []string{"/runs/", "/runs/run-1/unknown", "/runs/missing/memory", "/runs/missing/artifacts"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.Contains(t, rec.Body.String(), `"code":"not_found"`)
		})
	}
	for _, path := range []string{"/runs/missing/memory?agent_id=explicit", "/runs/missing/artifacts?agent_id=explicit"} {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
}

func TestDebugServerBindFailureLeavesShutdownSafe(t *testing.T) {
	_, err := NewServer(Config{})
	require.ErrorContains(t, err, "runtime is required")
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, listener.Close())
	})
	server, err := NewServer(Config{Runtime: agentsruntime.New(), Addr: listener.Addr().String()})
	require.NoError(t, err)
	require.Error(t, server.Start())
	assert.NoError(t, server.Shutdown(t.Context()))
}

func (failedDebugMemory) LoadRun(context.Context, string, string) (memory.Snapshot, error) {
	return memory.Snapshot{}, assert.AnError
}

func (s failedDebugArtifacts) List(context.Context, artifact.ListQuery) ([]artifact.Ref, error) {
	return nil, s.err
}

func (failedDebugEvents) List(context.Context, string, string, int) (runlog.Page, error) {
	return runlog.Page{}, assert.AnError
}
