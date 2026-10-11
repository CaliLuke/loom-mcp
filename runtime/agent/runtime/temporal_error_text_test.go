package runtime

import (
	"context"
	"encoding/json/v2"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// temporalToolErrorGolden is captured using the SDK's unconverted activity error:
// the exact ExecuteActivity.Get path used before E1. JSON strings preserve bytes.
type temporalToolErrorGolden struct {
	WorkflowError string `json:"workflow_error"`
	ToolError     string `json:"tool_error"`
	HookPayload   string `json:"hook_payload"`
}

func TestTemporalToolErrorTextGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/temporal_tool_error_text.golden.json")
	require.NoError(t, err)
	var fixtures map[string]temporalToolErrorGolden
	require.NoError(t, json.Unmarshal(data, &fixtures))
	for _, test := range []struct {
		name   string
		nested bool
	}{
		{name: "plain"}, {name: "nested", nested: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, ok := fixtures[test.name]
			require.True(t, ok)
			sdk := captureTemporalToolError(t, test.nested, false)
			assert.Equal(t, expected, sdk, "golden must still match the SDK's pre-E1 path")
			got := captureTemporalToolError(t, test.nested, true)
			assert.Equal(t, []byte(expected.WorkflowError), []byte(got.WorkflowError))
			assert.Equal(t, []byte(expected.ToolError), []byte(got.ToolError))
			assert.Equal(t, []byte(expected.HookPayload), []byte(got.HookPayload))
		})
	}
}

func captureTemporalToolError(t *testing.T, nested, adapted bool) temporalToolErrorGolden {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
		boom := temporal.NewNonRetryableApplicationError("boom", "example.boom", nil)
		if nested {
			return nil, temporal.NewNonRetryableApplicationError("outer", "example.outer", boom)
		}
		return nil, boom
	}, activity.RegisterOptions{Name: "failing"})
	eng, err := temporalengine.NewWorker(temporalengine.Options{
		ClientOptions:   &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)},
		WorkerOptions:   temporalengine.WorkerOptions{TaskQueue: "golden"},
		Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})
	wf := func(wctx workflow.Context) (temporalToolErrorGolden, error) {
		wfCtx, release := temporalengine.NewWorkflowContext(eng, wctx)
		defer release()
		input := &api.ToolInput{RunID: "golden-run", ToolCallID: "call", ToolName: "golden.tool"}
		var failure error
		if adapted {
			_, failure = wfCtx.ExecuteToolActivity(wfCtx.Context(), engine.ToolActivityCall{Name: "failing", Input: input, Options: engine.ActivityOptions{Queue: "golden", StartToCloseTimeout: time.Minute, RetryPolicy: engine.RetryPolicy{MaxAttempts: 1}}})
		} else {
			actx := workflow.WithActivityOptions(wctx, workflow.ActivityOptions{TaskQueue: "golden", StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
			var output *api.ToolOutput
			failure = workflow.ExecuteActivity(actx, "failing", input).Get(actx, &output)
		}
		require.Error(t, failure)
		return temporalToolErrorPayloads(t, failure), nil
	}
	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
	var got temporalToolErrorGolden
	require.NoError(t, env.GetWorkflowResult(&got))
	return got
}

// temporalToolErrorPayloads runs the actual runtime synthesis and hook codec so
// the golden covers recorded data, including the nested ToolError cause text.
func temporalToolErrorPayloads(t *testing.T, failure error) temporalToolErrorGolden {
	t.Helper()
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder))
	spec := newAnyJSONSpec("golden.tool", "golden")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "golden", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		panic("golden tool executor must not run")
	}}))
	exec := &toolBatchExec{r: rt, runID: "golden-run", agentID: "golden.agent", runCtx: &run.Context{RootRunID: "golden-run", RunID: "golden-run"}}
	call := planner.ToolRequest{Name: spec.Name, ToolCallID: "call", RunID: "golden-run"}
	collected := inspectCollectedExecutionFailure(failure)
	result, err := exec.synthesizeToolError(t.Context(), call, collected, "tool failed", 0)
	require.NoError(t, err)
	require.NotNil(t, result.Error)
	errorJSON, err := json.Marshal(result.Error)
	require.NoError(t, err)
	require.Len(t, recorder.events, 1)
	input, err := hooks.EncodeToHookInput(recorder.events[0], "")
	require.NoError(t, err)
	return temporalToolErrorGolden{WorkflowError: failure.Error(), ToolError: string(errorJSON), HookPayload: string(input.Payload)}
}
