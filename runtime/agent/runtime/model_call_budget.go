package runtime

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

const modelCallLimitErrorType = "loom_mcp.model_call_cap"

// resolvePlanModelCallLimit discards any successful planner presentation after a
// refused reservation while keeping all actual call evidence for adoption.
func resolvePlanModelCallLimit(ctx context.Context, events *runtimePlannerEvents, calls *CallRecording, recorder *modelRecoveryRecorder, input *PlanActivityInput, primary error) (*PlanActivityOutput, bool, error) {
	failure := calls.modelLimit()
	if failure == nil {
		return nil, false, nil
	}
	if err := modelCallLimitPrimaryError(ctx, primary); err != nil {
		return nil, true, err
	}
	events.discardModelPresentations(ctx)
	if err := events.hookError(); err != nil {
		return nil, true, err
	}
	usage, err := recorder.activityUsage(events.exportUsage())
	if err != nil {
		return nil, true, err
	}
	return &PlanActivityOutput{ModelCallLimit: failure, IndexHead: input.View.IndexHead, Usage: usage}, true, nil
}

func modelCallLimitError(failure *policy.ModelCallLimit) error {
	return engine.NewNonRetryableApplicationError(failure.Error(), modelCallLimitErrorType, failure, failure)
}

func isModelCallLimitError(err error) bool {
	return boundedErrorIs(err, policy.ErrModelCallLimit)
}

// modelCallLimitPrimaryError preserves cancellation and malformed errors before
// converting a refused attempt into durable policy evidence.
func modelCallLimitPrimaryError(ctx context.Context, primary error) error {
	primary = cancellation.Sanitize(primary)
	if ctx.Err() != nil {
		return errors.Join(primary, ctx.Err())
	}
	if cancellation.Contains(primary) || boundedErrorIs(primary, context.DeadlineExceeded) || boundedErrorIs(primary, cancellation.ErrInvalidErrorGraph) {
		return primary
	}
	return nil
}

func validatePlanModelCallLimit(out *PlanActivityOutput, input PlanActivityInput) error {
	if out.Result != nil || out.Recovery != nil || out.ContextOverflow != nil {
		return errors.New("model call limit output must not contain result or recovery")
	}
	if err := out.ModelCallLimit.Validate(); err != nil {
		return err
	}
	if out.ModelCallLimit.Call.RunID != input.RunID || out.ModelCallLimit.Limit != input.MaxModelCalls {
		return errors.New("model call limit evidence does not match activity input")
	}
	return model.ValidateTokenUsage(out.Usage)
}

func planOutputModelCallLimit(out *PlanActivityOutput) error {
	if out.ModelCallLimit != nil {
		return modelCallLimitError(out.ModelCallLimit)
	}
	return nil
}

// planActivityAccepted excludes every attempt replaced by policy evidence.
func planActivityAccepted(out *PlanActivityOutput) bool {
	return out.Recovery == nil && out.ContextOverflow == nil && out.ModelCallLimit == nil
}

// admitModelCall gates the already allocated durable reservation, never refunds it,
// and remembers the lowest refused ordinal independently of completion order.
func (r *CallRecording) admitModelCall(info model.CallInfo) error {
	if r.maxModelCalls == 0 || info.Seq <= r.maxModelCalls {
		return nil
	}
	failure := &policy.ModelCallLimit{Limit: r.maxModelCalls, Call: runstore.CallKey{RunID: info.RunID, Seq: info.Seq, Attempt: info.Attempt}}
	r.mu.Lock()
	if r.modelCallLimit == nil || failure.Call.Seq < r.modelCallLimit.Call.Seq {
		r.modelCallLimit = failure
	}
	r.mu.Unlock()
	return failure
}

// modelLimit returns an owned snapshot after the planner has joined its calls.
func (r *CallRecording) modelLimit() *policy.ModelCallLimit {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.modelCallLimit == nil {
		return nil
	}
	copy := *r.modelCallLimit
	return &copy
}

// recordAllocationFailure prevents a swallowed storage error from becoming a
// clean cap refusal or successful activity result.
func (r *CallRecording) recordAllocationFailure(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, err)
}

// recordModelCallUsage retains actual completed-attempt usage for terminal refusal.
func (r *CallRecording) recordModelCallUsage(usage model.TokenUsage) error {
	if r.maxModelCalls == 0 || r.activity != callrecord.ActivitySummarize {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	aggregate, err := checkedAddTokenUsage(r.usage, usage)
	if err != nil {
		return err
	}
	r.usage = aggregate
	return nil
}

func (r *CallRecording) modelCallUsage() model.TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// withModelCallAdmission keeps reservation allocation outside all middleware.
func (c *simplePlannerContext) withModelCallAdmission(client model.Client, id string) model.Client {
	return &callInfoClientWrapper{inner: client, store: c.rt.RunStore, recording: c.recording, info: model.CallInfo{
		AgentID: string(c.agent), RunID: c.runID, RootRunID: c.rootRunID,
		SessionID: c.sessionID, TurnID: c.turnID, ModelID: id, Attempt: c.attempt,
	}}
}
