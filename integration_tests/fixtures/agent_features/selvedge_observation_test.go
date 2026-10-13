package agentfeatures_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	sv "github.com/CaliLuke/selvedge"
	svhooks "github.com/CaliLuke/selvedge/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type selvedgeActivityObservation struct {
	ID, Name           string
	Input, Output      json.RawMessage
	Started, Completed time.Time
}

// selvedgeObservations captures actual live callbacks independently of exported history.
type selvedgeObservations struct {
	callbacks  atomic.Int64
	mu         sync.Mutex
	byInstance map[string][]selvedgeActivityObservation
	failures   []error
}

func TestSelvedgeHookCallbacksUnfiltered(t *testing.T) {
	o := &selvedgeObservations{}
	ctx := t.Context()
	o.OnActivityStart(ctx, svhooks.ActivityStartInfo{InstanceID: "run", ActivityID: "activity", IsReplay: true})
	o.OnActivityComplete(ctx, svhooks.ActivityCompleteInfo{InstanceID: "run", ActivityID: "activity", IsReplay: true})
	assert.Equal(t, int64(2), o.hookCalls(), "replay callbacks count before filtering")
	assert.Empty(t, o.snapshot("run"))

	o.OnActivityStart(ctx, svhooks.ActivityStartInfo{InstanceID: "run", ActivityID: "activity", InputData: "original"})
	o.OnActivityComplete(ctx, svhooks.ActivityCompleteInfo{InstanceID: "run", ActivityID: "activity", OutputData: "original"})
	o.OnActivityStart(ctx, svhooks.ActivityStartInfo{InstanceID: "run", ActivityID: "activity", InputData: "redispatch"})
	o.OnActivityComplete(ctx, svhooks.ActivityCompleteInfo{InstanceID: "run", ActivityID: "activity", OutputData: "redispatch"})
	assert.Equal(t, int64(6), o.hookCalls(), "duplicate identities still count callbacks")
	original := o.snapshot("run")
	require.Len(t, original, 1)
	assert.JSONEq(t, `"original"`, string(original[0].Input))
	assert.JSONEq(t, `"original"`, string(original[0].Output))

	o.OnWorkflowStart(ctx, svhooks.WorkflowStartInfo{})
	o.OnWorkflowComplete(ctx, svhooks.WorkflowCompleteInfo{})
	o.OnWorkflowFailed(ctx, svhooks.WorkflowFailedInfo{})
	o.OnWorkflowCancelled(ctx, svhooks.WorkflowCancelledInfo{})
	o.OnActivityFailed(ctx, svhooks.ActivityFailedInfo{})
	o.OnActivityRetry(ctx, svhooks.ActivityRetryInfo{})
	o.OnActivityCacheHit(ctx, svhooks.ActivityCacheHitInfo{})
	o.OnTimerStart(ctx, svhooks.TimerStartInfo{})
	o.OnTimerFired(ctx, svhooks.TimerFiredInfo{})
	o.OnReplayStart(ctx, svhooks.ReplayStartInfo{})
	o.OnReplayComplete(ctx, svhooks.ReplayCompleteInfo{})
	assert.Equal(t, int64(17), o.hookCalls())
}

func (o *selvedgeObservations) hookCalls() int64 {
	return o.callbacks.Load()
}

func (o *selvedgeObservations) OnActivityStart(_ context.Context, info svhooks.ActivityStartInfo) {
	o.callbacks.Add(1)
	if info.IsReplay {
		return
	}
	input, err := json.Marshal(info.InputData)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.failures = append(o.failures, err)
		return
	}
	if o.byInstance == nil {
		o.byInstance = make(map[string][]selvedgeActivityObservation)
	}
	// A cached command can be the last replay entry, clearing IsReplay before
	// this hook runs. Its durable key still identifies the original reservation.
	// Keep the first input; transient hook IDs/timestamps may be recomputed.
	for _, previous := range o.byInstance[info.InstanceID] {
		if previous.ID == info.ActivityID {
			return
		}
	}
	o.byInstance[info.InstanceID] = append(o.byInstance[info.InstanceID], selvedgeActivityObservation{ID: info.ActivityID, Name: info.ActivityName, Input: input, Started: time.Now()})
}
func (o *selvedgeObservations) OnActivityComplete(_ context.Context, info svhooks.ActivityCompleteInfo) {
	o.callbacks.Add(1)
	if info.IsReplay {
		return
	}
	output, err := json.Marshal(info.OutputData)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.failures = append(o.failures, err)
		return
	}
	observations := o.byInstance[info.InstanceID]
	for i := len(observations) - 1; i >= 0; i-- {
		if observations[i].ID == info.ActivityID && observations[i].Completed.IsZero() {
			observations[i].Output = output
			observations[i].Completed = time.Now()
			break
		}
	}
}
func (o *selvedgeObservations) snapshot(id string) []selvedgeActivityObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]selvedgeActivityObservation(nil), o.byInstance[id]...)
}
func (o *selvedgeObservations) OnWorkflowStart(_ context.Context, _ svhooks.WorkflowStartInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnWorkflowComplete(_ context.Context, _ svhooks.WorkflowCompleteInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnWorkflowFailed(_ context.Context, _ svhooks.WorkflowFailedInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnWorkflowCancelled(_ context.Context, _ svhooks.WorkflowCancelledInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnActivityFailed(_ context.Context, _ svhooks.ActivityFailedInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnActivityRetry(_ context.Context, _ svhooks.ActivityRetryInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnActivityCacheHit(_ context.Context, _ svhooks.ActivityCacheHitInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnTimerStart(_ context.Context, _ svhooks.TimerStartInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnTimerFired(_ context.Context, _ svhooks.TimerFiredInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnReplayStart(_ context.Context, _ svhooks.ReplayStartInfo) {
	o.callbacks.Add(1)
}
func (o *selvedgeObservations) OnReplayComplete(_ context.Context, _ svhooks.ReplayCompleteInfo) {
	o.callbacks.Add(1)
}

// observingSelvedgeEngine wraps registration only; all replay commands still go
// through the actual adapter and backend's read-only replay implementation.
type observingSelvedgeEngine struct {
	engine.Engine
	observed *replayCommandObservations
}
type replayCommandObservations struct {
	planners        []*api.PlanActivityInput
	outputs         []*api.PlanActivityOutput
	compatibilities []*contextcompat.Result
	summaries       []*api.SummarizeContextInput
}
type observingSelvedgeContext struct {
	engine.WorkflowContext
	observed *replayCommandObservations
}

func (e observingSelvedgeEngine) RegisterWorkflow(ctx context.Context, definition engine.WorkflowDefinition) error {
	handler := definition.Handler
	definition.Handler = func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		return handler(&observingSelvedgeContext{WorkflowContext: w, observed: e.observed}, in)
	}
	return e.Engine.RegisterWorkflow(ctx, definition)
}
func (w *observingSelvedgeContext) Context() context.Context {
	return engine.WithWorkflowContext(w.WorkflowContext.Context(), w)
}
func (w *observingSelvedgeContext) Await(ctx context.Context, condition func() bool) error {
	if engine.WorkflowContextFromContext(ctx) == w {
		ctx = engine.WithWorkflowContext(ctx, w.WorkflowContext)
	}
	return w.WorkflowContext.Await(ctx, condition)
}
func (w *observingSelvedgeContext) WithCancel() (engine.WorkflowContext, func()) {
	scope, cancel := w.WorkflowContext.WithCancel()
	return &observingSelvedgeContext{WorkflowContext: scope, observed: w.observed}, cancel
}
func (w *observingSelvedgeContext) Detached() engine.WorkflowContext {
	return &observingSelvedgeContext{WorkflowContext: w.WorkflowContext.Detached(), observed: w.observed}
}
func (w *observingSelvedgeContext) ExecutePlannerActivity(ctx context.Context, call engine.PlannerActivityCall) (*api.PlanActivityOutput, error) {
	cloned, err := cloneSelvedgeInput(call.Input)
	if err != nil {
		return nil, err
	}
	w.observed.planners = append(w.observed.planners, cloned)
	output, err := w.WorkflowContext.ExecutePlannerActivity(ctx, call)
	if err == nil && output != nil {
		cloned, cloneErr := cloneSelvedgeInput(output)
		if cloneErr != nil {
			return nil, cloneErr
		}
		w.observed.outputs = append(w.observed.outputs, cloned)
	}
	return output, err
}
func (w *observingSelvedgeContext) ExecuteSummarizeContextActivity(ctx context.Context, call engine.SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	cloned, err := cloneSelvedgeInput(call.Input)
	if err != nil {
		return nil, err
	}
	w.observed.summaries = append(w.observed.summaries, cloned)
	return w.WorkflowContext.ExecuteSummarizeContextActivity(ctx, call)
}
func cloneSelvedgeInput[T any](in *T) (*T, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func assertSelvedgeContextStart(t *testing.T, filename string, history *sv.RecordedHistory) api.RunInput {
	t.Helper()
	var start api.RunInput
	require.NoError(t, json.Unmarshal(history.Instance.Input, &start))
	assert.Equal(t, api.WorkflowProtocolVersion, start.WorkflowProtocolVersion)
	assert.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(compaction.Canonical(start.ContextSettings))), start.ContextFingerprint)
	assert.NotEmpty(t, start.ContextSettings.Prompt.Template)
	assert.NotEmpty(t, start.ContextSettings.Prompt.Ref.ID)
	if filename == "workflow_protocol_v19.json" || filename == "workflow_protocol_v19_summary.json" {
		require.NotNil(t, start.Policy)
		require.NotNil(t, start.Policy.Context)
		require.NotNil(t, start.Policy.Context.ContextWindow)
		assert.Equal(t, 5000, *start.Policy.Context.ContextWindow)
		assert.Equal(t, 5000, start.ContextSettings.ContextWindow)
	}
	return start
}

func assertSelvedgeObservedHistory(t *testing.T, filename string, history *sv.RecordedHistory, observations []selvedgeActivityObservation) {
	t.Helper()
	start := assertSelvedgeContextStart(t, filename, history)
	planners, summaries, resumes, adoptions := 0, 0, 0, 0
	budgetAdoption := false
	for _, observation := range observations {
		switch observation.Name {
		case "features.coordinator.plan", "features.coordinator.resume":
			var input api.PlanActivityInput
			require.NoError(t, json.Unmarshal(observation.Input, &input))
			assert.Equal(t, start.ContextFingerprint, input.View.ContextFingerprint)
			planners++
			if observation.Name == "features.coordinator.resume" {
				resumes++
				if filename == "workflow_protocol_v19_summary.json" {
					assert.Equal(t, 1, adoptions)
				}
				if filename == "workflow_protocol_v19_budget.json" {
					assert.Nil(t, input.Finalize)
					var output api.PlanActivityOutput
					require.NoError(t, json.Unmarshal(observation.Output, &output))
					require.NotNil(t, output.Result)
					require.NotNil(t, output.Result.FinalResponse)
					assert.Empty(t, output.Result.ToolCalls)
					assert.Nil(t, output.Result.Await)
				}
			}
		case "features.coordinator.summarize_context":
			var input api.SummarizeContextInput
			require.NoError(t, json.Unmarshal(observation.Input, &input))
			assert.Equal(t, start.ContextFingerprint, input.ContextFingerprint)
			assert.Equal(t, start.ContextSettings, input.ContextSettings)
			assert.Equal(t, "summary", input.ContextModel.ID)
			assert.Equal(t, 2, input.Step)
			assert.Greater(t, input.To, input.From)
			summaries++
		case "runtime.publish_hook":
			entries := historyHookInputs(t, observation.Input)
			for _, hook := range entries {
				if hook.Type == hooks.ContextCompacted {
					var adopted hooks.ContextCompactionEvent
					require.NoError(t, json.Unmarshal(hook.Payload, &adopted))
					if adopted.Tier == "summary" {
						adoptions++
						assert.NotEmpty(t, adopted.CallKeys)
						assert.NotNil(t, adopted.Checkpoint)
						assert.NotNil(t, adopted.Prompt)
						assert.Equal(t, 1, adopted.Chunks)
					}
				}
				if filename == "workflow_protocol_v19_budget.json" && hook.Type == hooks.PlannerStep {
					var step hooks.PlannerStepEvent
					require.NoError(t, json.Unmarshal(hook.Payload, &step))
					if step.Step == 2 {
						budgetAdoption = true
						require.NotNil(t, start.Policy)
						assert.Greater(t, observation.Completed.Sub(observation.Started), start.Policy.TimeBudget)
					}
				}
			}
		}
	}
	if filename != "workflow_protocol_v19_child_run.json" {
		assert.Positive(t, planners)
	}
	if filename == "workflow_protocol_v19_summary.json" {
		assert.Equal(t, 1, summaries)
		assert.Equal(t, 1, adoptions)
		assert.Equal(t, 1, resumes)
	}
	if filename == "workflow_protocol_v19_budget.json" {
		assert.True(t, budgetAdoption, "terminal adoption must complete after crossing the active budget")
		assert.Equal(t, 1, resumes)
	}
}

func (w *observingSelvedgeContext) ExecuteRecordContentActivity(ctx context.Context, call engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	output, err := w.WorkflowContext.ExecuteRecordContentActivity(ctx, call)
	if err == nil && output != nil && output.Compatibility != nil {
		cloned, cloneErr := cloneSelvedgeInput(output.Compatibility)
		if cloneErr != nil {
			return nil, cloneErr
		}
		w.observed.compatibilities = append(w.observed.compatibilities, cloned)
	}
	return output, err
}
