package opik_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestPublishedBeforeNetworkAndCorruptionFailsClosed(t *testing.T) {
	experiment := storedExperiment(t)
	remote := &remoteFixture{t: t, resources: make(map[string]jsontext.Value)}
	var manifestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(filepath.Join(experiment.Dir, "opik"))
		if !assert.NoError(t, err) || !assert.Len(t, entries, 1) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		manifestPath = filepath.Join(experiment.Dir, "opik", entries[0].Name())
		bytes, err := os.ReadFile(filepath.Clean(manifestPath))
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var manifest struct {
			Epoch int64 `json:"epoch"`
		}
		if !assert.NoError(t, json.Unmarshal(bytes, &manifest)) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		assert.InDelta(t, time.Now().UnixMilli(), manifest.Epoch, 5000)
		remote.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	id, err := uuid.Parse(result.ExperimentID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{"version":999}`), 0600))
	before := len(remote.requests)
	_, err = client.Export(t.Context(), experiment)
	require.Error(t, err)
	assert.Len(t, remote.requests, before)
}
