package opik_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type exportFailureWriter struct {
	err       error
	writes    int
	failAfter int
}

func TestExportSourceValidationBeforeNetwork(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*eval.Experiment)
		message string
	}{
		{"missing directory", func(e *eval.Experiment) {
			e.Dir = ""
		}, "directory required"},
		{"missing identity", func(e *eval.Experiment) {
			e.Header.ID = ""
		}, "identity"},
		{"unknown lifecycle", func(e *eval.Experiment) {
			e.Header.Status = "unknown"
		}, "status"},
		{"nil attempt", func(e *eval.Experiment) {
			e.Attempts = []*eval.StoredAttempt{nil}
		}, "nil stored attempt"},
		{"missing timestamp", func(e *eval.Experiment) {
			e.Header.Started = time.Time{}
			e.Attempts[0].Started = time.Time{}
		}, "no recorded start time"},
		{"corrupt raw evidence", func(e *eval.Experiment) {
			e.Attempts[0].Runs[0].Export = jsontext.Value(`{`)
		}, "opik run"},
		{"unencodable evaluation", func(e *eval.Experiment) {
			e.Attempts[0].Scores = []expect.Score{{Name: "score", Value: math.NaN()}}
		}, "NaN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
			require.NoError(t, err)
			experiment := storedExperiment(t)
			tt.mutate(experiment)
			_, err = client.Export(t.Context(), experiment)
			require.ErrorContains(t, err, tt.message)
			assert.Empty(t, remote.requests)
		})
	}
}

func TestExportPolicyFailuresAbortBeforePublication(t *testing.T) {
	policyErr := errors.New("application sharing decision failed")
	tests := []struct {
		name, kind string
		value      jsontext.Value
		err        error
		message    string
	}{
		{"evaluation callback", "evaluation", nil, policyErr, "sharing decision"},
		{"evaluation JSON", "evaluation", jsontext.Value(`{`), nil, "invalid policy JSON"},
		{"check explanation callback", "feedback_reason", nil, policyErr, "sharing decision"},
		{"check explanation type", "feedback_reason", jsontext.Value(`123`), nil, "JSON string"},
		{"judgment explanation callback", "judgment", nil, policyErr, "sharing decision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			experiment := storedExperiment(t)
			experiment.Attempts[0].Checks[0].Diagnostic = "explanation"
			if tt.kind == "judgment" {
				experiment.Attempts[0].Checks = nil
			}
			original := sharingPolicy()
			policy := runrecord.ProjectionPolicy{Name: "failure-boundary/v1", Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
				if c.Kind == tt.kind || (tt.kind == "judgment" && c.Kind == "feedback_reason") {
					return tt.value, true, tt.err
				}
				return original.Content(c)
			}}
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: policy})
			require.NoError(t, err)
			_, err = client.Export(t.Context(), experiment)
			require.ErrorContains(t, err, tt.message)
			assert.Empty(t, remote.requests)
		})
	}
}

func TestExportWarningsAndFailedJudgmentPreserveEvidenceDistinctions(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	experiment.Attempts[0].StorageError = "private storage diagnostic"
	experiment.Attempts[0].Runs[0].EventsUnverified = true
	experiment.Attempts[0].Runs = append(experiment.Attempts[0].Runs, eval.StoredRun{RunID: "missing"})
	experiment.Attempts[0].Judgments = append(experiment.Attempts[0].Judgments, eval.StoredJudgment{ClaimID: "unavailable", Error: "judge unavailable"})
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	require.Len(t, result.Attempts, 1)
	assert.True(t, result.Incomplete)
	assert.ElementsMatch(t, []string{"storage_error", "events_unverified", "missing_run_evidence"}, result.Attempts[0].Warnings)
	assert.Contains(t, strings.Join(remote.bodies, "\n"), "unavailable")
	assert.NotContains(t, strings.Join(remote.bodies, "\n"), "judgment/unavailable/")
}

func TestExportRepeatsShareOneDatasetItem(t *testing.T) {
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	experiment := storedExperiment(t)
	repeat := *experiment.Attempts[0]
	repeat.Repeat = 2
	repeat.Index = 1
	experiment.Attempts = append(experiment.Attempts, &repeat)
	experiment.Header.Planned = 2
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.Export(t.Context(), experiment)
	require.NoError(t, err)
	require.Len(t, result.Attempts, 2)
	assert.NotEqual(t, result.Attempts[0].ItemID, result.Attempts[1].ItemID)
	assert.Equal(t, 1, countRequest(remote.requests, "PUT /v1/private/datasets/items"))
}

func TestExportStoredLoadFailuresDoNotPublish(t *testing.T) {
	client, err := opik.New(opik.Config{Endpoint: "http://unused.invalid", Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	_, err = client.ExportStored(t.Context(), nil, "missing")
	require.ErrorContains(t, err, "local store required")
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	_, err = client.ExportStored(t.Context(), store, "unknown-reference")
	require.ErrorContains(t, err, "unknown experiment")
}

func TestExporterExtensionReportsOutputFailures(t *testing.T) {
	tests := []struct {
		name      string
		failAfter int
	}{{"JSON write", 0}, {"newline write", 1}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ref := pendingStoredExperiment(t)
			remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
			server := httptest.NewServer(remote)
			defer server.Close()
			client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
			require.NoError(t, err)
			writeErr := errors.New("output destination unavailable")
			writer := &exportFailureWriter{err: writeErr, failAfter: tt.failAfter}
			err = client.Extension().Run(t.Context(), evalcli.ExtensionRequest{Args: []string{ref}, Store: store, Stdout: writer})
			require.ErrorIs(t, err, writeErr)
		})
	}
}

func TestExporterExtensionRequiresOneReference(t *testing.T) {
	client, err := opik.New(opik.Config{Endpoint: "http://unused.invalid", Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	err = client.Extension().Run(t.Context(), evalcli.ExtensionRequest{})
	require.ErrorContains(t, err, "requires one")
}

func pendingStoredExperiment(t *testing.T) (*eval.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := eval.Open(dir)
	require.NoError(t, err)
	factory := eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
		return nil
	}, nil)
	config := eval.Config{Harness: factory, Scenarios: []eval.Scenario{{ID: "scenario", Agent: "agent", Expect: []expect.Expectation{expect.Completed()}, Items: eval.Items(eval.Item{ID: "item", Input: "question"})}}}
	writer, err := store.Begin(t.Context(), config, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	return store, writer.ID()
}

func (w *exportFailureWriter) Write(data []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, w.err
	}
	w.writes++
	return len(data), nil
}
