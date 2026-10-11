// Package enginetest provides reusable behavioral tests for workflow engines.
package enginetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// Factory describes an adapter's capabilities and builds an isolated fixture for
// each scenario. New must arrange cleanup through t.Cleanup. KnownGaps keys are
// exact scenario names; executable gaps must return GapError, while unexpressible
// scenarios are skipped. Values explain the semantic defect and follow-up.
type Factory struct {
	New       func(t *testing.T) Fixture
	Durable   bool
	Retries   bool
	KnownGaps map[string]string
}

// Fixture owns one fresh engine and its backend lifecycle. Backend-specific
// inspection stays in these hooks; conformance workflows use only engine APIs.
type Fixture struct {
	ctx           context.Context
	Engine        engine.Engine
	TaskQueue     string
	ActivityQueue string
	// Restart stops all old workers, creates a fresh engine on the same backend,
	// invokes register on it, and activates registration before returning.
	// Previously returned handles must remain usable (keep their backend client live).
	Restart func(t *testing.T, register func(engine.Engine)) engine.Engine
	// VerifyActivityOptions checks actual scheduled options, in invocation order.
	// It must inspect backend observations, not the values passed to registration.
	// Observed behavioral differences should return *GapError; backend errors
	// remain ordinary errors and cannot satisfy KnownGaps.
	VerifyActivityOptions func(t *testing.T, workflowID, activity string, want []engine.ActivityOptions) error
}

type scenario struct {
	name          string
	run           func(*testing.T, Fixture) error
	capability    string
	unexpressible bool
}

const (
	testRunID   = "run"
	rootRunID   = "root"
	echoTool    = "tools.echo"
	indexRef    = "sha256:index"
	testCallID  = "call"
	childName   = "child"
	labelKey    = "key"
	labelValue  = "value"
	cleanupName = "cleanup"

	workflowName       = "conformance"
	activityName       = "conformance.activity"
	applicationFailure = "enginetest application failure"
)

// RunConformance checks the Engine and WorkflowContext behavioral contract.
// Unsupported capabilities and unexpressible API gaps are visible skips.
// Executable known gaps must reproduce a behavioral failure; a fixed gap fails
// the test until it is removed from Factory.KnownGaps. Missing lifecycle or
// inspection hooks fail rather than silently passing a test.
func RunConformance(t *testing.T, factory Factory) {
	t.Helper()
	require.NotNil(t, factory.New)
	cases := make([]scenario, 0, 49)
	cases = append(cases, []scenario{
		{name: "lifecycle", run: testLifecycle},
		{name: "id_conflict", run: testIDConflict},
		{name: "tools_parallel", run: testParallelTools},
		{name: "tools_parallel_error", run: testParallelToolFailure},
		{name: "timer_now", run: testTimerNow},
		{name: "child_success", run: testChildSuccess},
		{name: "child_failure", run: testChildFailure},
		{name: "parent_cancels_child", run: testParentCancelsChild},
		{name: "external_cancel_cleanup", run: testCancellation},
		{name: "cancel_scope", run: testCancelScope},
		{name: "queries", run: testQueries},
		{name: "await_signal", run: testAwaitSignal},
		{name: "durable_restart", run: testRestart, capability: "Durable"},
	}...)
	cases = append(cases, activityScenarios()...)
	cases = append(cases, signalScenarios()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			t.Cleanup(cancel)
			runScenario(t, ctx, factory, tc)
		})
	}
}

// runScenario accepts only completed behavioral observations as known-gap evidence.
func runScenario(t *testing.T, ctx context.Context, factory Factory, tc scenario) {
	t.Helper()
	if tc.capability == "Durable" && !factory.Durable {
		t.Skip("capability Durable=false: backend does not persist workflow history")
	}
	if tc.capability == "Retries" && !factory.Retries {
		t.Skip("capability Retries=false: backend does not retry activities")
	}
	reason, knownGap := factory.KnownGaps[tc.name]
	if knownGap {
		require.NotEmpty(t, reason)
		if tc.unexpressible {
			t.Skipf("KNOWN_API_GAP[%s]: %s", tc.name, reason)
		}
	}
	fixture := factory.New(t)
	require.NotNil(t, fixture.Engine)
	fixture.ctx = ctx
	err := tc.run(t, fixture)
	if knownGap {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("known gap scenario did not complete: context=%v; observed=%v", ctx.Err(), err)
		}
		require.Error(t, err, "known gap no longer reproduces: remove it from the list")
		var gap *GapError
		require.ErrorAs(t, err, &gap, "known gap scenario must return a typed behavioral failure")
		t.Logf("EXPECTED_FAILURE[%s]: %s; observed: %v", tc.name, reason, err)
		return
	}
	require.NoError(t, err)
}

func registerWorkflow(t *testing.T, f Fixture, name string, fn engine.WorkflowFunc) {
	t.Helper()
	require.NoError(t, f.Engine.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: name, TaskQueue: f.TaskQueue, Handler: fn}))
}

func start(t *testing.T, f Fixture, ctx context.Context) engine.WorkflowHandle {
	t.Helper()
	if sealer, ok := f.Engine.(engine.RegistrationSealer); ok {
		require.NoError(t, sealer.SealRegistration(ctx))
	}
	h, err := f.Engine.StartWorkflow(ctx, request(f))
	require.NoError(t, err)
	t.Cleanup(func() {
		err := h.Cancel(context.WithoutCancel(ctx))
		if !errors.Is(err, engine.ErrWorkflowCompleted) {
			assert.NoError(t, err)
		}
	})
	return h
}

func request(f Fixture) engine.WorkflowStartRequest {
	return engine.WorkflowStartRequest{ID: f.TaskQueue + "-run", Workflow: workflowName, TaskQueue: f.TaskQueue,
		Input: &api.RunInput{RunID: f.TaskQueue + "-run", RootRunID: rootRunID, SessionID: "session", Labels: map[string]string{labelKey: labelValue}}, RunTimeout: time.Minute}
}

// waitSuccess returns behavioral failures so known gaps can be checked without
// marking testing.T failed. Registration and fixture setup still fail directly.
func waitSuccess(ctx context.Context, h engine.WorkflowHandle) error {
	out, err := h.Wait(ctx)
	if err != nil {
		return err
	}
	return checkEqual(success(), out)
}

func checkApplicationFailure(err error) error {
	if err == nil {
		return &GapError{Err: errors.New("application failure did not propagate: workflow succeeded")}
	}
	if !strings.Contains(err.Error(), applicationFailure) {
		return &GapError{Err: fmt.Errorf("application failure did not propagate: %w", err)}
	}
	return nil
}

func checkCanceled(err error) error {
	if err == nil {
		return &GapError{Err: errors.New("cancellation did not propagate: workflow succeeded")}
	}
	if !errors.Is(err, context.Canceled) {
		return &GapError{Err: fmt.Errorf("cancellation did not propagate: %w", err)}
	}
	return nil
}

func receive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatalf("barrier: %v", ctx.Err())
		var zero T
		return zero
	}
}

func checkEqual(want, got any) error {
	if !assert.ObjectsAreEqual(want, got) {
		return &GapError{Err: fmt.Errorf("boundary values: want %#v, got %#v", want, got)}
	}
	return nil
}

func success() *api.RunOutput {
	return &api.RunOutput{RunID: "conformance-run", FinalRef: "sha256:final", ToolEventsRef: "sha256:events", ToolEventCount: 3}
}

func activityOptions(f Fixture) engine.ActivityOptions {
	return engine.ActivityOptions{Queue: f.ActivityQueue, StartToCloseTimeout: 10 * time.Second,
		ScheduleToStartTimeout: 10 * time.Second, HeartbeatTimeout: 5 * time.Second,
		RetryPolicy: engine.RetryPolicy{MaxAttempts: 1, InitialInterval: time.Millisecond, BackoffCoefficient: 1}}
}
