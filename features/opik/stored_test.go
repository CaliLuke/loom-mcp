package opik_test

import (
	"bytes"
	"context"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"encoding/json/jsontext"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportStoredAttemptBeforeCompletionLine(t *testing.T) {
	storeDir := t.TempDir()
	store, err := eval.Open(storeDir)
	require.NoError(t, err)
	var captured *runtime.Runtime
	factory := func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		return agenttest.NewHarness(ctx, func(ctx context.Context, rt *runtime.Runtime) error {
			captured = rt
			return nil
		}, opts...)
	}
	cfg := eval.Config{AttemptHarness: factory, Scenarios: []eval.Scenario{{ID: "scenario", Agent: "agent", Expect: []expect.Expectation{expect.Completed()}, Items: eval.Items(eval.Item{ID: "item", Input: "question"})}}}
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	key := eval.AttemptKey{Variant: eval.DefaultVariant, Scenario: "scenario", Item: "item", Repeat: 1}
	harness, err := writer.CaptureHarness(factory)(t.Context(), eval.AttemptInfo{Key: key})
	require.NoError(t, err)
	parts, _, err := callrecord.BuildRequest(t.Context(), captured.RunStore, "root", &model.Request{Model: "fake", Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "unfinished-question"}}}}}, index.ViewRef{}, nil)
	require.NoError(t, err)
	callKey := runstore.CallKey{RunID: "root", Seq: 1, Attempt: 1}
	started, err := callrecord.Encode(callrecord.Started{Key: callKey, Model: "fake", Request: parts, Started: time.Now()})
	require.NoError(t, err)
	require.NoError(t, captured.RunStore.PutCall(t.Context(), "root", runstore.CallRecord{Key: callKey, State: runstore.CallStarted, Started: started}))
	childKey := runstore.CallKey{RunID: "unlinked-child", Seq: 1, Attempt: 1}
	childStarted, err := callrecord.Encode(callrecord.Started{Key: childKey, Model: "fake", Request: parts, Started: time.Now()})
	require.NoError(t, err)
	require.NoError(t, captured.RunStore.PutCall(t.Context(), "root", runstore.CallRecord{Key: childKey, State: runstore.CallStarted, Started: childStarted}))
	require.NoError(t, harness.Close())
	loaded, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.Empty(t, loaded.Attempts)
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.ExportStored(t.Context(), store, writer.ID())
	require.NoError(t, err)
	require.Len(t, result.Attempts, 1)
	assert.True(t, result.Incomplete)
	assert.True(t, result.Attempts[0].Incomplete)
	assert.Contains(t, result.Attempts[0].Warnings, "missing_attempt_completion")
	assert.Contains(t, result.Attempts[0].Warnings, "incomplete_call")
	assert.Contains(t, result.Attempts[0].Warnings, "unlinked_run_evidence")
	assert.Contains(t, result.Attempts[0].Warnings, "unsealed_run")
	assert.Contains(t, remote.bodies[2], "unfinished-question")
	assert.Contains(t, remote.bodies[2], "unlinked-child")
	var output, diagnostic bytes.Buffer
	code := evalcli.Main(t.Context(), []string{"opik-export", writer.ID()}, evalcli.Config{StoreDir: storeDir, Stdout: &output, Stderr: &diagnostic, Extensions: []evalcli.Extension{client.Extension()}})
	assert.Equal(t, 0, code)
	assert.Empty(t, diagnostic.String())
	assert.Contains(t, output.String(), "missing_attempt_completion")
}

func TestExportStoredCaptureReadFailureRemainsExplicit(t *testing.T) {
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	factory := func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		return agenttest.NewHarness(ctx, func(context.Context, *runtime.Runtime) error {
			return nil
		}, opts...)
	}
	cfg := eval.Config{AttemptHarness: factory, Scenarios: []eval.Scenario{{ID: "scenario", Agent: "agent", Expect: []expect.Expectation{expect.Completed()}, Items: eval.Items(eval.Item{ID: "item", Input: "question"})}}}
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	key := eval.AttemptKey{Variant: eval.DefaultVariant, Scenario: "scenario", Item: "item", Repeat: 1}
	harness, err := writer.CaptureHarness(factory)(t.Context(), eval.AttemptInfo{Key: key})
	require.NoError(t, err)
	dir := harness.EvidenceDir()
	require.NoError(t, harness.Close())
	require.NoError(t, os.Remove(filepath.Join(dir, "evidence.sqlite")))
	remote := &remoteFixture{t: t, resources: map[string]jsontext.Value{}}
	server := httptest.NewServer(remote)
	defer server.Close()
	client, err := opik.New(opik.Config{Endpoint: server.URL, Project: "review", Policy: sharingPolicy()})
	require.NoError(t, err)
	result, err := client.ExportStored(t.Context(), store, writer.ID())
	require.Error(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Incomplete)
	require.Len(t, result.Attempts, 1)
	assert.Contains(t, result.Attempts[0].Warnings, "capture_read_error")
	assert.Contains(t, result.Attempts[0].Warnings, "missing_run_evidence")
}
