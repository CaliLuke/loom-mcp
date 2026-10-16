package opik_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteFixture struct {
	t         *testing.T
	mu        sync.Mutex
	resources map[string]jsontext.Value
	requests  []string
	bodies    []string
	failItem  bool
	lostTrace bool
	lostItem  bool
}

func TestExportProtocolRetryAndProjection(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}, failItem: true}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	source := filepath.Join(experiment.Dir, "local-evidence")
	require.NoError(t, os.WriteFile(source, []byte("unchanged"), 0600))
	client, err := opik.New(opik.Config{Endpoint: server.URL + "/api", Project: "review", Workspace: "default", Policy: sharingPolicy()})
	require.NoError(t, err)
	first, err := client.Export(t.Context(), experiment)
	require.Error(t, err)
	require.NotNil(t, first)
	require.Len(t, first.Attempts, 1)
	firstTrace := first.Attempts[0].TraceID
	assert.Equal(t, uuid.Version(7), uuid.MustParse(firstTrace).Version())
	reopened, err := opik.New(opik.Config{Endpoint: server.URL + "/api", Project: "review", Workspace: "default", Policy: sharingPolicy()})
	require.NoError(t, err)
	second, err := reopened.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.Equal(t, firstTrace, second.Attempts[0].TraceID)
	assert.Equal(t, first.ExperimentID, second.ExperimentID)
	assert.Equal(t, uuid.Version(7), uuid.MustParse(second.Attempts[0].ItemID).Version())
	assert.False(t, second.Incomplete)
	all := strings.Join(remote.bodies, "\n")
	assert.Contains(t, all, "public-answer")
	assert.NotContains(t, all, "private-reason")
	assert.NotContains(t, all, "private-meta")
	assert.Contains(t, all, "trace-local")
	assert.Contains(t, all, "judge-key")
	assert.Contains(t, all, "recorded-revision")
	assert.Contains(t, all, "recorded-build-v1")
	assert.Contains(t, all, "feedback-scores")
	assert.Contains(t, all, "9007199254740993")
	assert.Equal(t, 1, countRequest(remote.requests, "POST /api/v1/private/traces"))
	assert.Equal(t, 2, countRequest(remote.requests, "POST /api/v1/private/experiments/items"))
	bytes, err := os.ReadFile(filepath.Clean(source))
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(bytes))
}

func TestExportLostTraceResponse(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}, lostTrace: true}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	first, err := client.Export(t.Context(), experiment)
	require.Error(t, err)
	second, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.Equal(t, first.Attempts[0].TraceID, second.Attempts[0].TraceID)
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/traces"))
}

func TestExportMissingEvidenceAndPolicyFailure(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	experiment.Attempts[0].Runs = nil
	experiment.TruncatedTail = true
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.True(t, result.Incomplete)
	assert.Contains(t, result.Attempts[0].Warnings, "missing_run_evidence")
	rejected := storedExperiment(t)
	config := opik.Config{Endpoint: server.URL, Project: "review", Policy: runrecord.ProjectionPolicy{Name: "reject/v1", Content: func(runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return nil, false, fmt.Errorf("rejected")
	}}}
	failing, err := opik.New(config)
	require.NoError(t, err)
	before := len(remote.requests)
	_, err = failing.Export(t.Context(), rejected)
	require.Error(t, err)
	assert.Len(t, remote.requests, before)
}

func TestExportRemoteIdentityConflict(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	first, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	remote.mu.Lock()
	remote.resources["/v1/private/traces/"+first.Attempts[0].TraceID] = jsontext.Value(`{"id":"wrong","metadata":{}}`)
	remote.mu.Unlock()
	second, err := client.Export(t.Context(), experiment)
	require.ErrorContains(t, err, "identity mismatch")
	assert.Equal(t, first.Attempts[0].TraceID, second.Attempts[0].TraceID)
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/traces"))
}

func TestExportEvaluationSharingPolicy(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	experiment.Attempts[0].Task = "private-task"
	experiment.Attempts[0].Judgments[0].Rationale = "private-rationale"
	policy := sharingPolicy()
	original := policy.Content
	policy.Name = "application/restrict-evaluation/v1"
	policy.Content = func(content runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		if content.Kind == "evaluation" || content.Kind == "feedback_reason" {
			return nil, false, nil
		}
		return original(content)
	}
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: policy})
	require.NoError(t, err)
	_, err = client.Export(t.Context(), experiment)
	require.NoError(t, err)
	all := strings.Join(remote.bodies, "\n")
	assert.NotContains(t, all, "private-task")
	assert.NotContains(t, all, "private-rationale")
	assert.Contains(t, all, "judgment/claim/0")
	assert.Contains(t, all, `"label":"entailed"`)
}

func TestExportCancellationAndManifestStorageFailure(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.Export(ctx, storedExperiment(t))
	require.ErrorIs(t, err, context.Canceled)
	blocked := storedExperiment(t)
	require.NoError(t, os.WriteFile(filepath.Join(blocked.Dir, "opik"), []byte("blocked"), 0600))
	_, err = client.Export(t.Context(), blocked)
	require.Error(t, err)
	assert.Empty(t, remote.requests)
}

func TestExportVerifiesAllRemoteProvenance(t *testing.T) {
	for _, field := range []string{"agent", "dataset_hash", "source_identity", "source_fingerprint", "judge", "judgments", "otel_trace_ids"} {
		t.Run(field, func(t *testing.T) {
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			experiment := storedExperiment(t)
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
			require.NoError(t, err)
			result, err := client.Export(t.Context(), experiment)
			require.NoError(t, err)
			path := "/v1/private/traces/" + result.Attempts[0].TraceID
			var trace map[string]jsontext.Value
			remote.mu.Lock()
			bytes := remote.resources[path]
			remote.mu.Unlock()
			require.NoError(t, json.Unmarshal(bytes, &trace))
			var metadata map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(trace["metadata"], &metadata))
			metadata[field] = jsontext.Value(`"conflicting-provenance"`)
			trace["metadata"], err = json.Marshal(metadata)
			require.NoError(t, err)
			encoded, err := json.Marshal(trace)
			require.NoError(t, err)
			remote.mu.Lock()
			remote.resources[path] = encoded
			remote.mu.Unlock()
			_, err = client.Export(t.Context(), experiment)
			require.ErrorContains(t, err, "metadata mismatch")
		})
	}
}

func TestExportPreservesExperimentLifecycle(t *testing.T) {
	for _, test := range []struct{ local, remote string }{{"running", "running"}, {"stopped", "cancelled"}, {"complete", "completed"}} {
		t.Run(test.local, func(t *testing.T) {
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			experiment := storedExperiment(t)
			experiment.Header.Status = test.local
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
			require.NoError(t, err)
			result, err := client.Export(t.Context(), experiment)
			require.NoError(t, err)
			var body map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(remote.resources["/v1/private/experiments/"+result.ExperimentID], &body))
			assert.Equal(t, test.remote, stringValue(t, body["status"]))
			var metadata map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(body["metadata"], &metadata))
			assert.Equal(t, test.local, stringValue(t, metadata["source_status"]))
			body["status"] = jsontext.Value(`"wrong"`)
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			remote.mu.Lock()
			remote.resources["/v1/private/experiments/"+result.ExperimentID] = encoded
			remote.mu.Unlock()
			_, err = client.Export(t.Context(), experiment)
			require.ErrorContains(t, err, "identity mismatch at status")
		})
	}
}

func TestExportEquivalentEscapedIdentity(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "rev/iew", Policy: sharingPolicy()})
	require.NoError(t, err)
	first, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	path := "/v1/private/experiments/" + first.ExperimentID
	var body map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(remote.resources[path], &body))
	body["project_name"] = jsontext.Value(`"\u0072ev\/iew"`)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	encoded = bytes.Replace(encoded, []byte(`"project_name":"rev/iew"`), []byte(`"project_name":"\u0072ev\/iew"`), 1)
	require.Contains(t, string(encoded), `\u0072`)
	remote.mu.Lock()
	remote.resources[path] = encoded
	remote.mu.Unlock()
	_, err = client.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/experiments"))
}

func TestExportLostAttemptResponse(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}, lostItem: true}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	first, err := client.Export(t.Context(), experiment)
	require.Error(t, err)
	second, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	assert.Equal(t, first.Attempts[0].ItemID, second.Attempts[0].ItemID)
	assert.Equal(t, 1, countRequest(remote.requests, "POST /v1/private/experiments/items"))
}

func (r *remoteFixture) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := request.Method + " " + request.URL.Path
	r.requests = append(r.requests, key)
	path := strings.TrimPrefix(request.URL.Path, "/api")
	if request.Method == http.MethodGet {
		if data, ok := r.resources[path]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write(data)
			assert.NoError(r.t, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var body map[string]jsontext.Value
	if !assert.NoError(r.t, json.UnmarshalRead(request.Body, &body)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	bytes, err := json.Marshal(body)
	if !assert.NoError(r.t, err) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.bodies = append(r.bodies, key+" "+string(bytes))
	if path == "/v1/private/experiments/items" {
		if r.failItem {
			r.failItem = false
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var items []map[string]jsontext.Value
		if !assert.NoError(r.t, json.Unmarshal(body["experiment_items"], &items)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !assert.Len(r.t, items, 1) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var id string
		if !assert.NoError(r.t, json.Unmarshal(items[0]["id"], &id)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		encoded, err := json.Marshal(items[0])
		if !assert.NoError(r.t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.resources[path+"/"+id] = encoded
		if r.lostItem {
			r.lostItem = false
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if path == "/v1/private/datasets/items" {
		assert.Equal(r.t, http.MethodPut, request.Method)
		assert.NotEmpty(r.t, body["dataset_name"])
		assert.NotEmpty(r.t, body["items"])
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.Contains(path, "/feedback-scores") {
		assert.Equal(r.t, http.MethodPut, request.Method)
		assert.NotEmpty(r.t, body["name"])
		assert.Equal(r.t, `"sdk"`, string(body["source"]))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var id string
	if !assert.NoError(r.t, json.Unmarshal(body["id"], &id)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	parsedID, parseErr := uuid.Parse(id)
	if !assert.NoError(r.t, parseErr) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	assert.Equal(r.t, uuid.Version(7), parsedID.Version())
	r.resources[path+"/"+id] = bytes
	if path == "/v1/private/traces" {
		assert.NotEmpty(r.t, body["start_time"])
		if r.lostTrace {
			r.lostTrace = false
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}

func storedExperiment(t *testing.T) *eval.Experiment {
	t.Helper()
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Sealed: true, Transcript: []*model.Message{{Role: model.ConversationRoleAssistant, Meta: map[string]any{"secret": "private-meta"}, Parts: []model.Part{model.TextPart{Text: "public-answer"}, model.ThinkingPart{Text: "private-reason"}, model.ToolUsePart{ID: "tool", Name: "lookup", Input: jsontext.Value(`{"n":9007199254740993}`)}}}}}}}
	raw, err := record.Export().Marshal()
	require.NoError(t, err)
	return &eval.Experiment{Dir: t.TempDir(), Header: eval.ExperimentHeader{ID: uuid.NewString(), Status: "complete", Source: eval.VCS{Revision: "recorded-revision", Modified: true, Origin: "buildinfo"}, Versions: eval.Versions{MainVersion: "recorded-build-v1"}, Planned: 1, Started: time.Unix(100, 0), Scenarios: []eval.ScenarioInfo{{ID: "scenario", Agent: "agent", DatasetHash: "dataset-hash"}}, Judge: &eval.JudgeInfo{CalibrationKey: "judge-key", Calibrated: true}}, Attempts: []*eval.StoredAttempt{{AttemptKey: eval.AttemptKey{Scenario: "scenario", Item: "item", Variant: "variant", Repeat: 1}, Index: 0, Started: time.Unix(100, 0), Duration: time.Second, Verdict: eval.VerdictPassed, Checks: []expect.Check{{Name: "check", Passed: true}}, Scores: []expect.Score{{Name: "efficiency", Value: 0.8}}, Judgments: []eval.StoredJudgment{{ClaimID: "claim", Label: "entailed", Rationale: "approved-reason"}}, Runs: []eval.StoredRun{{RunID: "root", TraceID: "trace-local", Export: raw}}}}}
}

func sharingPolicy() runrecord.ProjectionPolicy {
	return runrecord.ProjectionPolicy{Name: "test/public/v1", Content: func(content runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		switch content.Kind {
		case "reasoning", "metadata", "server_only", "attachment":
			return nil, false, nil
		}
		return content.Value, true, nil
	}}
}

func stringValue(t *testing.T, data jsontext.Value) string {
	t.Helper()
	var value string
	require.NoError(t, json.Unmarshal(data, &value))
	return value
}

func countRequest(requests []string, target string) int {
	count := 0
	for _, request := range requests {
		if request == target {
			count++
		}
	}
	return count
}
