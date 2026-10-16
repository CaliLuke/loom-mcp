package agenttest_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/internal/localevidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type evidenceBlockingProvider struct{}

type evidenceCancelProvider struct {
	entered chan struct{}
}

func TestEvidenceCompletedAndFailedCalls(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			dir := t.TempDir()
			turn := agenttest.Respond(agenttest.Text("done"))
			if failure {
				turn = agenttest.Fail(errors.New("model broke"))
			}
			h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.WithEvidenceDir(dir), agenttest.ScriptedModel("model", turn))
			require.NoError(t, err)
			result, err := h.Run(t.Context(), "root", agenttest.User("original request"))
			require.NoError(t, err)
			require.NoError(t, h.Close())
			snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
			require.NoError(t, err)
			require.Len(t, snapshot.Records, 1)
			assert.Empty(t, snapshot.Unlinked)
			want, err := result.Record.Export().Marshal()
			require.NoError(t, err)
			got, err := snapshot.Records[0].Export().Marshal()
			require.NoError(t, err)
			assert.Equal(t, want, got)
			_, err = agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.WithEvidenceDir(dir))
			require.ErrorIs(t, err, os.ErrExist)
		})
	}
}

func TestEvidenceConstructionFailureAndOptions(t *testing.T) {
	dir := t.TempDir()
	_, err := agenttest.NewHarness(t.Context(), func(context.Context, *runtime.Runtime) error {
		return errors.New("register failed")
	}, agenttest.WithEvidenceDir(dir))
	require.ErrorContains(t, err, "register failed")
	snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, snapshot.Records)
	for _, opts := range [][]agenttest.Option{
		{agenttest.WithEvidenceDir("")},
		{agenttest.WithEvidenceDir(t.TempDir()), agenttest.WithEvidenceDir(t.TempDir())},
	} {
		_, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{}), opts...)
		require.Error(t, err)
	}
}

func TestEvidenceChildRuns(t *testing.T) {
	dir := t.TempDir()
	h, err := agenttest.NewHarness(t.Context(), registerChildHarness(harnessPlanner{id: "child"}), agenttest.WithEvidenceDir(dir), agenttest.ScriptedModel("child", agenttest.Respond(agenttest.Text("done"))))
	require.NoError(t, err)
	result, err := h.Run(t.Context(), "parent", nil)
	require.NoError(t, err)
	require.NoError(t, h.Close())
	snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	require.Len(t, snapshot.Records[0].Runs, 2)
	assert.Empty(t, snapshot.Unlinked)
	child := snapshot.Records[0].Runs[1]
	assert.Equal(t, result.Record.Runs[1].RunID, child.RunID)
	require.Len(t, child.Calls, 1)
	assert.Equal(t, runrecord.Adopted, child.Calls[0].Class)
}

func TestEvidenceUnlinkedCallRun(t *testing.T) {
	dir := t.TempDir()
	store, err := localevidence.Create(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, store.RegisterRoot(t.Context(), "root"))
	request := &model.Request{Model: "fake"}
	parts, _, err := callrecord.BuildRequest(t.Context(), store, "root", request, index.ViewRef{}, nil)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: "unlinked-child", Seq: 1, Attempt: 1}
	started, err := callrecord.Encode(callrecord.Started{Key: key, Step: 1, Activity: callrecord.ActivityPlanner, Model: "fake", Request: parts})
	require.NoError(t, err)
	require.NoError(t, store.PutCall(t.Context(), "root", runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}))
	require.NoError(t, store.Close())
	snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	assert.Empty(t, snapshot.Records[0].Runs[0].Calls)
	require.Len(t, snapshot.Unlinked, 1)
	assert.Equal(t, key, snapshot.Unlinked[0].Runs[0].Calls[0].Record.Key)
	assert.Equal(t, runrecord.Incomplete, snapshot.Unlinked[0].Runs[0].Calls[0].Class)
}

func TestEvidenceCloseWaitsForCanceledCall(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	provider, err := model.NewClient(evidenceCancelProvider{entered: entered})
	require.NoError(t, err)
	h, err := agenttest.NewHarness(t.Context(), func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("model", provider); err != nil {
			return err
		}
		return registerHarnessAgent("root", harnessPlanner{id: "model"})(ctx, rt)
	}, agenttest.WithEvidenceDir(dir))
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		_, err := h.Run(t.Context(), "root", agenttest.User("cancel me"))
		finished <- err
	}()
	<-entered
	require.ErrorContains(t, h.Close(), "Wait has not returned")
	require.NoError(t, <-finished)
	snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	calls := snapshot.Records[0].Runs[0].Calls
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].Completion)
	assert.Equal(t, runstore.CallFailed, calls[0].Record.State)
	assert.Equal(t, "canceled", calls[0].Completion.Error.Kind)
}

func TestEvidenceSubprocess(t *testing.T) {
	dir, mode := os.Getenv("LOOM_EVIDENCE_DIR"), os.Getenv("LOOM_EVIDENCE_MODE")
	if mode == "capture" {
		provider, err := model.NewClient(evidenceBlockingProvider{})
		require.NoError(t, err)
		h, err := agenttest.NewHarness(t.Context(), func(ctx context.Context, rt *runtime.Runtime) error {
			if err := rt.RegisterModel("model", provider); err != nil {
				return err
			}
			return registerHarnessAgent("root", harnessPlanner{id: "model"})(ctx, rt)
		}, agenttest.WithEvidenceDir(dir))
		require.NoError(t, err)
		_, err = h.Run(t.Context(), "root", agenttest.User("request survives abrupt exit"))
		t.Fatal("blocked provider returned", err)
	}
	if mode == "read" {
		snapshot, err := agenttest.ReadEvidence(t.Context(), dir)
		require.NoError(t, err)
		require.Len(t, snapshot.Records, 1)
		run := snapshot.Records[0].Runs[0]
		require.Len(t, run.Calls, 1)
		assert.False(t, run.Sealed)
		assert.Equal(t, runrecord.Incomplete, run.Calls[0].Class)
		assert.Nil(t, run.Calls[0].Completion)
		assert.Equal(t, "request survives abrupt exit", run.Calls[0].Request.Messages[0].Parts[0].(model.TextPart).Text)
		return
	}
	dir = t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(dir, "evidence-test-helper")))
	command := exec.CommandContext(t.Context(), "./evidence-test-helper", "-test.run=^TestEvidenceSubprocess$")
	command.Dir = dir
	command.Env = append(os.Environ(), "LOOM_EVIDENCE_DIR="+dir, "LOOM_EVIDENCE_MODE=capture")
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan())
	assert.Equal(t, "provider entered", scanner.Text())
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	reader := exec.CommandContext(t.Context(), "./evidence-test-helper", "-test.run=^TestEvidenceSubprocess$")
	reader.Dir = dir
	reader.Env = append(os.Environ(), "LOOM_EVIDENCE_DIR="+dir, "LOOM_EVIDENCE_MODE=read")
	output, err := reader.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func (evidenceBlockingProvider) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	if _, err := fmt.Fprintln(os.Stdout, "provider entered"); err != nil {
		return nil, err
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (evidenceBlockingProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (p evidenceCancelProvider) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (evidenceCancelProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}
