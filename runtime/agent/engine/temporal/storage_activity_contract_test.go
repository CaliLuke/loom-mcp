package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type storageTestWorker struct {
	*workflowTestWorker
}

type storageActivityCase struct {
	name     string
	input    any
	output   any
	register func(*Engine, string, engine.ActivityOptions, func(context.Context, any) error) error
	execute  func(engine.WorkflowContext, string, engine.ActivityOptions, bool) (any, error)
}

func TestTemporalStorageActivitiesRoundTrip(t *testing.T) {
	for _, activityCase := range storageActivityCases() {
		for _, optionsCase := range []struct {
			name  string
			queue string
			call  engine.ActivityOptions
		}{
			{name: "registered queue", queue: "storage.queue"},
			{name: "default queue"},
			{name: "call overrides", queue: "storage.queue", call: engine.ActivityOptions{Queue: "override.queue", StartToCloseTimeout: 9 * time.Second, ScheduleToStartTimeout: 5 * time.Second, HeartbeatTimeout: 2 * time.Second}},
		} {
			for _, fail := range []bool{false, true} {
				name := "success"
				if fail {
					name = "activity failure"
				}
				t.Run(activityCase.name+"/"+optionsCase.name+"/"+name, func(t *testing.T) {
					testStorageActivityRoundTrip(t, activityCase, optionsCase.queue, optionsCase.call, fail)
				})
			}
		}
	}
}

func TestTemporalStorageActivityValidation(t *testing.T) {
	for _, tc := range storageActivityCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, eng := newStorageTestEnvironment(t)
			callback := func(context.Context, any) error {
				t.Error("invalid registration must not invoke the activity")
				return nil
			}
			require.ErrorContains(t, tc.register(eng, "", engine.ActivityOptions{}, callback), "activity name cannot be empty")
			require.ErrorContains(t, tc.register(&Engine{}, "activity", engine.ActivityOptions{}, callback), "client mode cannot")
			wf := &temporalWorkflowContext{baseCtx: t.Context()}
			out, err := tc.execute(wf, "", engine.ActivityOptions{}, false)
			assert.Nil(t, out)
			require.ErrorContains(t, err, "activity name is required")
			out, err = tc.execute(wf, "activity", engine.ActivityOptions{}, true)
			assert.Nil(t, out)
			require.ErrorContains(t, err, "activity input is required")
		})
	}
}

// testStorageActivityRoundTrip invokes registered engine wrappers through the SDK,
// checking serialization, scheduling, context injection and terminal activity errors.
func testStorageActivityRoundTrip(t *testing.T, tc storageActivityCase, queue string, callOpts engine.ActivityOptions, fail bool) {
	t.Helper()
	env, eng := newStorageTestEnvironment(t)
	eng.activityDefaults.Hook = ActivityTimeoutDefaults{QueueWaitTimeout: 11 * time.Second, LivenessTimeout: 3 * time.Second}
	opts := engine.ActivityOptions{Queue: queue, StartToCloseTimeout: 15 * time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 1}}
	attempts := 0
	require.NoError(t, tc.register(eng, "storage.activity", opts, func(ctx context.Context, input any) error {
		attempts++
		assert.Equal(t, tc.input, input)
		info := activity.GetInfo(ctx)
		assert.Equal(t, "storage.activity", info.ActivityType.Name)
		assert.Equal(t, expectedStorageQueue(eng, queue, callOpts.Queue), info.TaskQueue)
		start, heartbeat := 15*time.Second, 3*time.Second
		if callOpts.StartToCloseTimeout > 0 {
			start, heartbeat = callOpts.StartToCloseTimeout, callOpts.HeartbeatTimeout
		}
		assert.Equal(t, start, info.StartToCloseTimeout)
		assert.Equal(t, heartbeat, info.HeartbeatTimeout)
		owner := engine.WorkflowContextFromContext(ctx)
		if assert.NotNil(t, owner, "registration wrapper injects the workflow context") {
			assert.Equal(t, info.WorkflowExecution.ID, owner.WorkflowID())
		}
		if fail {
			return errors.New("store unavailable")
		}
		return nil
	}))
	registered := eng.activityOptions["storage.activity"]
	assert.Equal(t, 11*time.Second, registered.ScheduleToStartTimeout)
	assert.Equal(t, 3*time.Second, registered.HeartbeatTimeout)
	assert.Equal(t, opts.Queue, registered.Queue)
	assert.Equal(t, opts.RetryPolicy, registered.RetryPolicy)
	assert.Contains(t, eng.workers, expectedStorageQueue(eng, queue, ""))
	var got any
	require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "storage.workflow", Handler: func(wf engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		var err error
		got, err = tc.execute(wf, "storage.activity", callOpts, false)
		return &api.RunOutput{RunID: "parent"}, err
	}}))
	env.ExecuteWorkflow("storage.workflow", &api.RunInput{RunID: "parent"})
	assert.Equal(t, 1, attempts, "configured activity retry limit is respected")
	if fail {
		require.ErrorContains(t, env.GetWorkflowError(), "store unavailable")
		assert.Nil(t, got)
	} else {
		require.NoError(t, env.GetWorkflowError())
		assert.Equal(t, tc.output, got)
	}
}

// newStorageTestEnvironment routes worker registrations into the SDK environment;
// no activity function is registered directly or replaced with a mock result.
func newStorageTestEnvironment(t *testing.T) (*testsuite.TestWorkflowEnvironment, *Engine) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(NewAgentDataConverter(nil))
	eng := newTestEngine(t)
	eng.workerFactory = func(client.Client, string, worker.Options) worker.Worker {
		return &storageTestWorker{workflowTestWorker: &workflowTestWorker{fakeWorker: &fakeWorker{}, env: env}}
	}
	return env, eng
}

func expectedStorageQueue(eng *Engine, registered, override string) string {
	if override != "" {
		return override
	}
	if registered != "" {
		return registered
	}
	return eng.defaultQueue
}

// storageActivityCases keeps concrete API types at each register/execute boundary.
func storageActivityCases() []storageActivityCase {
	childIn := &api.ChildDispatchInput{Call: planner.ToolRequest{Name: "child.lookup", ToolCallID: "call", Payload: rawjson.Message(`{"query":"item"}`)}, ParentRun: run.Context{RootRunID: "root", RunID: "parent"}, View: index.ViewRef{IndexHead: "sha256:previous", Pending: []index.MessageEntry{{ID: 2, Kind: index.Assistant, Calls: []runstore.Hash{"sha256:call"}, Chain: "sha256:chain"}}}}
	childOut := &api.ChildDispatchOutput{IndexHead: "sha256:committed", Error: "evidence required", RetryHint: &planner.RetryHint{Message: "provide evidence"}}
	contentIn := &api.RecordContentInput{RootRunID: "root", Items: [][]byte{[]byte("first"), []byte("second")}, SeedMessages: true}
	contentOut := &api.RecordContentOutput{Refs: []runstore.Hash{"sha256:first", "sha256:second"}, IndexHead: "sha256:seed"}
	finishIn := &api.FinishRunInput{RootRunID: "root", ToolEvents: []*api.ToolEvent{{Name: "child.lookup", ToolCallID: "call", Refs: tools.ResultRefs{Result: "sha256:result"}}}}
	finishOut := &api.FinishRunOutput{FinalRef: "sha256:final", ToolEventsRef: "sha256:events", ToolEventCount: 1}
	sealIn := &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "parent", IndexHead: "sha256:previous", Pending: childIn.View.Pending, EventKey: "run_sealed:parent", Timestamp: time.Unix(42, 123456789).UTC()}
	sealOut := &api.SealRecordOutput{IndexHead: "sha256:sealed"}
	return []storageActivityCase{
		{name: "child dispatch", input: childIn, output: childOut, register: func(eng *Engine, name string, opts engine.ActivityOptions, check func(context.Context, any) error) error {
			return eng.RegisterChildDispatchActivity(context.Background(), name, opts, func(ctx context.Context, in *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
				if err := check(ctx, in); err != nil {
					return nil, err
				}
				return childOut, nil
			})
		}, execute: func(wf engine.WorkflowContext, name string, opts engine.ActivityOptions, missing bool) (any, error) {
			in := childIn
			if missing {
				in = nil
			}
			return wf.ExecuteChildDispatchActivity(wf.Context(), engine.ChildDispatchActivityCall{Name: name, Options: opts, Input: in})
		}},
		{name: "record content", input: contentIn, output: contentOut, register: func(eng *Engine, name string, opts engine.ActivityOptions, check func(context.Context, any) error) error {
			return eng.RegisterRecordContentActivity(context.Background(), name, opts, func(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
				if err := check(ctx, in); err != nil {
					return nil, err
				}
				return contentOut, nil
			})
		}, execute: func(wf engine.WorkflowContext, name string, opts engine.ActivityOptions, missing bool) (any, error) {
			in := contentIn
			if missing {
				in = nil
			}
			return wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{Name: name, Options: opts, Input: in})
		}},
		{name: "finish run", input: finishIn, output: finishOut, register: func(eng *Engine, name string, opts engine.ActivityOptions, check func(context.Context, any) error) error {
			return eng.RegisterFinishRunActivity(context.Background(), name, opts, func(ctx context.Context, in *api.FinishRunInput) (*api.FinishRunOutput, error) {
				if err := check(ctx, in); err != nil {
					return nil, err
				}
				return finishOut, nil
			})
		}, execute: func(wf engine.WorkflowContext, name string, opts engine.ActivityOptions, missing bool) (any, error) {
			in := finishIn
			if missing {
				in = nil
			}
			return wf.ExecuteFinishRunActivity(wf.Context(), engine.FinishRunActivityCall{Name: name, Options: opts, Input: in})
		}},
		{name: "seal record", input: sealIn, output: sealOut, register: func(eng *Engine, name string, opts engine.ActivityOptions, check func(context.Context, any) error) error {
			return eng.RegisterSealRecordActivity(context.Background(), name, opts, func(ctx context.Context, in *api.SealRecordInput) (*api.SealRecordOutput, error) {
				if err := check(ctx, in); err != nil {
					return nil, err
				}
				return sealOut, nil
			})
		}, execute: func(wf engine.WorkflowContext, name string, opts engine.ActivityOptions, missing bool) (any, error) {
			in := sealIn
			if missing {
				in = nil
			}
			return wf.ExecuteSealRecordActivity(wf.Context(), engine.SealRecordActivityCall{Name: name, Options: opts, Input: in})
		}},
	}
}

func (w *storageTestWorker) RegisterActivityWithOptions(fn any, opts activity.RegisterOptions) {
	w.env.RegisterActivityWithOptions(fn, opts)
}
