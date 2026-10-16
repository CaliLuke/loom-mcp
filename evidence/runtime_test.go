package evidence_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicEvidenceProvider struct {
	err     error
	entered chan struct{}
	blocked bool
}

type publicEvidencePlanner struct{}

type publicEvidenceParentPlanner struct{}

func TestOrdinaryRuntimeCapturesCompletedAndFailedCalls(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provider  publicEvidenceProvider
		wantState runstore.CallState
	}{
		{name: "completed", wantState: runstore.CallCompleted},
		{name: "failed", provider: publicEvidenceProvider{err: errors.New("model failed")}, wantState: runstore.CallFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "capture")
			store, err := evidence.Create(t.Context(), dir)
			require.NoError(t, err)
			rt := runtime.New(runtime.WithEngine(engineinmem.New()), runtime.WithRunStore(store), runtime.WithRunEventStore(store))
			provider, err := model.NewClient(tc.provider)
			require.NoError(t, err)
			require.NoError(t, rt.RegisterModel("model", provider))
			require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
				ID: "root", Planner: publicEvidencePlanner{},
				Workflow:         engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
				PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool",
			}))
			_, runErr := rt.MustClient("root").OneShotRun(t.Context(), []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "keep this request"}}}}, runtime.WithRunID("ordinary-root"))
			if tc.provider.err == nil {
				require.NoError(t, runErr)
			} else {
				require.ErrorContains(t, runErr, "model failed")
			}
			require.NoError(t, store.Close())
			snapshot, err := evidence.Read(t.Context(), dir)
			require.NoError(t, err)
			require.Len(t, snapshot.Records, 1)
			assert.Empty(t, snapshot.Unlinked)
			run := snapshot.Records[0].Runs[0]
			assert.Equal(t, "ordinary-root", run.RunID)
			require.Len(t, run.Calls, 1)
			assert.Equal(t, tc.wantState, run.Calls[0].Record.State)
			assert.Equal(t, "ordinary-root", run.Calls[0].Record.Key.RunID)
			assert.Equal(t, "keep this request", run.Calls[0].Request.Messages[0].Parts[0].(model.TextPart).Text)
			if tc.provider.err == nil {
				assert.Equal(t, runrecord.Adopted, run.Calls[0].Class)
			}
		})
	}
}

func TestOrdinaryRuntimeRetainsChildIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	store, err := evidence.Create(t.Context(), dir)
	require.NoError(t, err)
	rt := runtime.New(runtime.WithEngine(engineinmem.New()), runtime.WithRunStore(store), runtime.WithRunEventStore(store))
	provider, err := model.NewClient(publicEvidenceProvider{})
	require.NoError(t, err)
	require.NoError(t, rt.RegisterModel("model", provider))
	require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
		ID: "child", Planner: publicEvidencePlanner{},
		Workflow:         engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool",
	}))
	spec := tools.ToolSpec{Name: "delegate.child", Toolset: "delegate", IsAgentTool: true, AgentID: "child", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
	registration := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "delegate", AgentID: "child", Route: runtime.AgentRoute{ID: "child", WorkflowName: "child.workflow", DefaultTaskQueue: "queue"}})
	registration.Specs = []tools.ToolSpec{spec}
	require.NoError(t, rt.RegisterToolset(registration))
	require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
		ID: "parent", Planner: publicEvidenceParentPlanner{}, Specs: []tools.ToolSpec{spec},
		Workflow:         engine.WorkflowDefinition{Name: "parent.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "parent.plan", ResumeActivityName: "parent.resume", ExecuteToolActivity: "parent.tool",
	}))
	_, err = rt.MustClient("parent").OneShotRun(t.Context(), nil, runtime.WithRunID("ordinary-parent"))
	require.NoError(t, err)
	require.NoError(t, store.Close())
	snapshot, err := evidence.Read(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	assert.Empty(t, snapshot.Unlinked)
	require.Len(t, snapshot.Records[0].Runs, 2)
	assert.Equal(t, "ordinary-parent", snapshot.Records[0].Runs[0].RunID)
	child := snapshot.Records[0].Runs[1]
	assert.NotEmpty(t, child.RunID)
	assert.NotEqual(t, "ordinary-parent", child.RunID)
	require.Len(t, child.Calls, 1)
	assert.Equal(t, child.RunID, child.Calls[0].Record.Key.RunID)
}

func TestOrdinaryRuntimeCancellationIsFailedEvidence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	entered := make(chan struct{})
	store, rt := publicEvidenceRuntime(t, dir, publicEvidenceProvider{entered: entered})
	_, err := rt.CreateSession(t.Context(), "cancel-session")
	require.NoError(t, err)
	handle, err := rt.MustClient("root").Start(t.Context(), "cancel-session", []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "cancel request"}}}}, runtime.WithRunID("cancel-root"))
	require.NoError(t, err)
	<-entered
	require.NoError(t, handle.Cancel(t.Context()))
	_, err = handle.Wait(t.Context())
	require.Error(t, err)
	require.NoError(t, store.Close())
	snapshot, err := evidence.Read(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	require.Len(t, snapshot.Records[0].Runs[0].Calls, 1)
	call := snapshot.Records[0].Runs[0].Calls[0]
	assert.Equal(t, runstore.CallFailed, call.Record.State)
	require.NotNil(t, call.Completion)
	assert.Equal(t, "canceled", call.Completion.Error.Kind)
}

func TestOrdinaryRuntimeSurfacesEvidenceStorageFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	store, rt := publicEvidenceRuntime(t, dir, publicEvidenceProvider{})
	require.NoError(t, store.Close())
	_, err := rt.MustClient("root").OneShotRun(t.Context(), nil, runtime.WithRunID("closed-store"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "database is closed")
}

func TestOrdinaryRuntimeInterruptedRequestIsReadable(t *testing.T) {
	dir := os.Getenv("LOOM_PUBLIC_EVIDENCE_DIR")
	if os.Getenv("LOOM_PUBLIC_EVIDENCE_CHILD") == "1" {
		_, rt := publicEvidenceRuntime(t, dir, publicEvidenceProvider{blocked: true})
		_, err := rt.MustClient("root").OneShotRun(t.Context(), []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "interrupted request"}}}}, runtime.WithRunID("interrupted-root"))
		t.Fatal("blocked provider returned", err)
	}
	workDir := t.TempDir()
	dir = filepath.Join(workDir, "capture")
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(workDir, "evidence-test-helper")))
	childCtx, cancelChild := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancelChild()
	command := exec.CommandContext(childCtx, "./evidence-test-helper", "-test.run=^TestOrdinaryRuntimeInterruptedRequestIsReadable$")
	command.Dir = workDir
	command.Env = append(os.Environ(), "LOOM_PUBLIC_EVIDENCE_CHILD=1", "LOOM_PUBLIC_EVIDENCE_DIR="+dir)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	reaped := false
	defer func() {
		if reaped {
			return
		}
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		if err := command.Wait(); err == nil {
			t.Error("interrupted child unexpectedly exited successfully")
		}
	}()
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan(), "child did not report persisted request: scanner=%v context=%v", scanner.Err(), childCtx.Err())
	assert.Equal(t, "provider entered", scanner.Text())
	killErr := command.Process.Kill()
	waitErr := command.Wait()
	reaped = true
	require.NoError(t, killErr)
	require.Error(t, waitErr)
	snapshot, err := evidence.Read(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
	run := snapshot.Records[0].Runs[0]
	assert.Equal(t, "interrupted-root", run.RunID)
	assert.False(t, run.Sealed)
	require.Len(t, run.Calls, 1)
	assert.Equal(t, runrecord.Incomplete, run.Calls[0].Class)
	assert.Nil(t, run.Calls[0].Completion)
	assert.Equal(t, "interrupted request", run.Calls[0].Request.Messages[0].Parts[0].(model.TextPart).Text)
}

func publicEvidenceRuntime(t *testing.T, dir string, p publicEvidenceProvider) (*evidence.Store, *runtime.Runtime) {
	t.Helper()
	store, err := evidence.Create(t.Context(), dir)
	require.NoError(t, err)
	rt := runtime.New(runtime.WithEngine(engineinmem.New()), runtime.WithRunStore(store), runtime.WithRunEventStore(store))
	provider, err := model.NewClient(p)
	require.NoError(t, err)
	require.NoError(t, rt.RegisterModel("model", provider))
	require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
		ID: "root", Planner: publicEvidencePlanner{},
		Workflow:         engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool",
	}))
	return store, rt
}

func (publicEvidencePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return publicEvidenceComplete(ctx, in.Agent, in.Messages)
}

func (publicEvidencePlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return publicEvidenceComplete(ctx, in.Agent, in.Messages)
}

func (publicEvidenceParentPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "delegate.child", ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
}

func (publicEvidenceParentPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "parent done"}}}}}, nil
}

func publicEvidenceComplete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("model")
	if !ok {
		return nil, errors.New("model missing")
	}
	response, err := client.Complete(ctx, &model.Request{Model: "model", Messages: messages})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &response.Content[0]}}, nil
}

func (p publicEvidenceProvider) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	if p.blocked {
		if _, err := fmt.Fprintln(os.Stdout, "provider entered"); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.entered != nil {
		close(p.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.err != nil {
		return nil, p.err
	}
	return &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}, nil
}

func (publicEvidenceProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}
