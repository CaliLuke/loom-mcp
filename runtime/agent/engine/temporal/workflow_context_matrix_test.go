package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

type workflowContextProbe struct {
	WorkflowID          string
	RunID               string
	ContextCarriesOwner bool
	ImmediateReady      bool
	TimerAdvanced       bool
	CanceledTimer       bool
	ChildRunID          string
	ChildOutputRunID    string
}
type childRunIDProbe struct {
	BeforeGet   string
	AfterGet    string
	ExecutionID string
}

func TestTemporalWorkflowContextLifecycleAndChildIdentity(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	eng := workflowContextTestEngine()
	childWorkflow := func(ctx workflow.Context, input *api.RunInput) (*api.RunOutput, error) {
		return &api.RunOutput{RunID: input.RunID}, nil
	}
	env.RegisterWorkflowWithOptions(childWorkflow, workflow.RegisterOptions{Name: "childWorkflow"})
	parentWorkflow := func(ctx workflow.Context) (workflowContextProbe, error) {
		wf := newTemporalWorkflowContext(eng, ctx)
		probe := workflowContextProbe{
			WorkflowID:          wf.WorkflowID(),
			RunID:               wf.RunID(),
			ContextCarriesOwner: engine.WorkflowContextFromContext(wf.Context()) == wf,
		}
		startedAt := wf.Now()
		immediate, err := wf.NewTimer(context.Background(), 0)
		if err != nil {
			return probe, err
		}
		probe.ImmediateReady = immediate.IsReady()
		if _, err := immediate.Get(context.Background()); err != nil {
			return probe, err
		}
		timer, err := wf.NewTimer(context.Background(), time.Second)
		if err != nil {
			return probe, err
		}
		firedAt, err := timer.Get(context.Background())
		if err != nil {
			return probe, err
		}
		probe.TimerAdvanced = firedAt.Equal(startedAt.Add(time.Second))

		cancelable, cancel := wf.WithCancel()
		canceledTimer, err := cancelable.NewTimer(context.Background(), time.Hour)
		if err != nil {
			return probe, err
		}
		cancel()
		_, err = canceledTimer.Get(context.Background())
		probe.CanceledTimer = errors.Is(err, context.Canceled)

		child, err := wf.StartChildWorkflow(context.Background(), engine.ChildWorkflowRequest{
			ID:       "child-workflow-id",
			Workflow: "childWorkflow",
			Input:    &api.RunInput{RunID: "child-output-run"},
		})
		if err != nil {
			return probe, err
		}
		probe.ChildRunID = child.RunID()
		out, err := child.Get(context.Background())
		if err != nil {
			return probe, err
		}
		probe.ChildOutputRunID = out.RunID
		return probe, nil
	}

	env.ExecuteWorkflow(parentWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var probe workflowContextProbe
	require.NoError(t, env.GetWorkflowResult(&probe))
	assert.NotEmpty(t, probe.WorkflowID)
	assert.NotEmpty(t, probe.RunID)
	assert.True(t, probe.ContextCarriesOwner)
	assert.True(t, probe.ImmediateReady)
	assert.True(t, probe.TimerAdvanced)
	assert.True(t, probe.CanceledTimer)
	assert.NotEmpty(t, probe.ChildRunID)
	assert.Equal(t, "child-output-run", probe.ChildOutputRunID)
}
func TestTemporalChildHandleRunIDMatchesExecution(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	childWorkflow := func(ctx workflow.Context) (*api.RunOutput, error) {
		return &api.RunOutput{RunID: workflow.GetInfo(ctx).WorkflowExecution.RunID}, nil
	}
	env.RegisterWorkflowWithOptions(childWorkflow, workflow.RegisterOptions{Name: "runIDChildWorkflow"})
	parentWorkflow := func(ctx workflow.Context) (childRunIDProbe, error) {
		wf := newTemporalWorkflowContext(workflowContextTestEngine(), ctx)
		child, err := wf.StartChildWorkflow(context.Background(), engine.ChildWorkflowRequest{
			ID:       "run-id-child-workflow-id",
			Workflow: "runIDChildWorkflow",
		})
		if err != nil {
			return childRunIDProbe{}, err
		}
		probe := childRunIDProbe{BeforeGet: child.RunID()}
		out, err := child.Get(context.Background())
		if err != nil {
			return probe, err
		}
		probe.AfterGet = child.RunID()
		probe.ExecutionID = out.RunID
		return probe, nil
	}

	env.ExecuteWorkflow(parentWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var probe childRunIDProbe
	require.NoError(t, env.GetWorkflowResult(&probe))
	require.NotEmpty(t, probe.ExecutionID)
	assert.Equal(t, probe.ExecutionID, probe.BeforeGet)
	assert.Equal(t, probe.ExecutionID, probe.AfterGet)
}

func TestNewWorkflowContextReleasePreservesReplacementAndRemovesRegistryEntry(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	eng := workflowContextTestEngine()
	externalWorkflow := func(ctx workflow.Context) (bool, error) {
		_, releaseFirst := NewWorkflowContext(eng, ctx)
		wfCtx, release := NewWorkflowContext(eng, ctx)
		defer release()

		releaseFirst()
		tracked, ok := eng.workflowContexts.Load(wfCtx.RunID())
		return ok && tracked == wfCtx, nil
	}

	env.ExecuteWorkflow(externalWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var replacementSurvivedStaleRelease bool
	require.NoError(t, env.GetWorkflowResult(&replacementSurvivedStaleRelease))
	assert.True(t, replacementSurvivedStaleRelease)

	remaining := 0
	eng.workflowContexts.Range(func(_, _ any) bool {
		remaining++
		return true
	})
	assert.Zero(t, remaining)
}

func TestTemporalWorkflowContextReceivesAllSignalsAndTimesOut(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	eng := workflowContextTestEngine()
	signalWorkflow := func(ctx workflow.Context) (bool, error) {
		wf := newTemporalWorkflowContext(eng, ctx)
		if _, err := wf.PauseRequests().ReceiveWithTimeout(context.Background(), 5*time.Second); err != nil {
			return false, err
		}
		if _, err := wf.ResumeRequests().Receive(context.Background()); err != nil {
			return false, err
		}
		if _, err := wf.ClarificationAnswers().Receive(context.Background()); err != nil {
			return false, err
		}
		if _, err := wf.ExternalToolResults().Receive(context.Background()); err != nil {
			return false, err
		}
		if _, err := wf.ConfirmationDecisions().Receive(context.Background()); err != nil {
			return false, err
		}
		if _, err := wf.TypedInputAnswers().Receive(context.Background()); err != nil {
			return false, err
		}
		_, err := wf.PauseRequests().ReceiveWithTimeout(context.Background(), time.Second)
		return errors.Is(err, context.DeadlineExceeded), nil
	}

	for signal, value := range map[string]any{
		api.SignalPause:                &api.PauseRequest{},
		api.SignalResume:               &api.ResumeRequest{},
		api.SignalProvideClarification: &api.ClarificationAnswer{},
		api.SignalProvideToolResults:   &api.ToolResultsSet{},
		api.SignalProvideConfirmation:  &api.ConfirmationDecision{},
		api.SignalProvideTypedInput:    &api.TypedInputAnswer{},
	} {
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(signal, value)
		}, time.Second)
	}

	env.ExecuteWorkflow(signalWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var timedOut bool
	require.NoError(t, env.GetWorkflowResult(&timedOut))
	assert.True(t, timedOut)
}

func TestTemporalWorkflowContextReceiveStopsOnWorkflowCancellation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	eng := workflowContextTestEngine()
	cancelWorkflow := func(ctx workflow.Context) (bool, error) {
		wf := newTemporalWorkflowContext(eng, ctx)
		cancelable, cancel := wf.WithCancel()
		workflow.Go(ctx, func(ctx workflow.Context) {
			_ = workflow.NewTimer(ctx, time.Second).Get(ctx, nil)
			cancel()
		})
		_, err := cancelable.PauseRequests().Receive(context.Background())
		return errors.Is(err, context.Canceled), nil
	}

	env.ExecuteWorkflow(cancelWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var canceled bool
	require.NoError(t, env.GetWorkflowResult(&canceled))
	assert.True(t, canceled)
}

func TestTemporalWorkflowContextValidationAndImmediateFuture(t *testing.T) {
	wf := &temporalWorkflowContext{}
	require.EqualError(t, wf.PublishHook(context.Background(), engine.HookActivityCall{}), "hook activity name is required")
	require.EqualError(t, wf.PublishHook(context.Background(), engine.HookActivityCall{Name: "hook"}), "hook activity input is required")
	_, err := wf.ExecutePlannerActivity(context.Background(), engine.PlannerActivityCall{})
	require.EqualError(t, err, "planner activity name is required")
	_, err = wf.ExecutePlannerActivity(context.Background(), engine.PlannerActivityCall{Name: "planner"})
	require.EqualError(t, err, "planner activity input is required")
	_, err = wf.ExecuteToolActivityAsync(context.Background(), engine.ToolActivityCall{})
	require.EqualError(t, err, "tool activity name is required")
	_, err = wf.ExecuteToolActivityAsync(context.Background(), engine.ToolActivityCall{Name: "tool"})
	require.EqualError(t, err, "tool activity input is required")
	require.EqualError(t, wf.Await(context.Background(), nil), "await condition is required")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (immediateFuture[string]{v: "ready"}).Get(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, (immediateFuture[string]{v: "ready"}).IsReady())
	require.NoError(t, normalizeTemporalError(nil))
	ordinary := errors.New("ordinary")
	assert.ErrorIs(t, normalizeTemporalError(ordinary), ordinary)
}

func workflowContextTestEngine() *Engine {
	return &Engine{
		defaultQueue:    "test-queue",
		activityOptions: make(map[string]engine.ActivityOptions),
		logger:          telemetry.NewNoopLogger(),
		metrics:         telemetry.NewNoopMetrics(),
		tracer:          telemetry.NewNoopTracer(),
	}
}
