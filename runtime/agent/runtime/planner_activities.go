package runtime

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/reminder"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
)

const plannerThoughtCodeKey = "code"

// PlanStartActivity executes the planner's PlanStart method.
//
// Advanced & generated integration
//   - Intended to be registered by generated code with the workflow engine.
//   - Normal applications should use AgentClient (Runtime.Client(...).Run/Start)
//     instead of invoking activities directly.
//
// This activity is registered with the workflow engine and invoked at the
// beginning of a run to produce the initial plan. The activity creates an
// agent context with memory access and delegates to the planner's PlanStart
// implementation.
func (r *Runtime) PlanStartActivity(ctx context.Context, input *PlanActivityInput) (out *PlanActivityOutput, activityErr error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	view, err := r.loadView(ctx, input.RunContext.RootRunID, input.RunID, input.View)
	if err != nil {
		return nil, err
	}
	input = loadedPlanActivityInput(input, view)
	defer returnLoadedView(&out, view)
	if err := r.restoreRecoveryCorrection(ctx, input); err != nil {
		return nil, err
	}
	events := newPlannerEvents(r, input.AgentID, input.RunID, input.RunContext.SessionID, input.RunContext.TurnID)
	events.step = input.Step
	recoveryRecorder := &modelRecoveryRecorder{}
	recordedView := input.View
	recordedView.IndexHead, recordedView.Pending = view.Head, nil
	callRecording := NewCallRecording(r.RunStore, input.RunContext.RootRunID, recordedView, view.Messages, input.Step)
	callRecording.configureOverflow(input)
	defer callRecording.finishActivity(ctx, &out, &activityErr, events)
	reg, agentCtx, err := r.plannerContext(ctx, input, events, recoveryRecorder, callRecording)
	if err != nil {
		return nil, err
	}
	planInput, err := r.startPlanInput(ctx, reg, agentCtx, events, input, view)
	if err != nil {
		return nil, err
	}
	result, err := r.planStart(ctx, reg, planInput)
	if out, handled, err := resolvePlanActivityOverflow(ctx, events, callRecording, recoveryRecorder, input); handled {
		return out, err
	}
	if out, handled, err := r.resolvePlanActivityRecovery(ctx, events, recoveryRecorder, input, err); handled {
		return out, err
	}
	r.logger.Info(ctx, "PlanStartActivity returning PlanResult", "tool_calls", len(result.ToolCalls), "final_response", result.FinalResponse != nil, "await", result.Await != nil)
	return r.completePlanActivity(ctx, result, input, events, recoveryRecorder)
}

// PlanResumeActivity executes the planner's PlanResume method.
//
// Advanced & generated integration
//   - Intended to be registered by generated code with the workflow engine.
//   - Normal applications should use AgentClient (Runtime.Client(...).Run/Start)
//     instead of invoking activities directly.
//
// This activity is registered with the workflow engine and invoked after tool
// execution to produce the next plan. The activity creates an agent context
// with memory access and delegates to the planner's PlanResume implementation.
func (r *Runtime) PlanResumeActivity(ctx context.Context, input *PlanActivityInput) (out *PlanActivityOutput, activityErr error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	view, err := r.loadView(ctx, input.RunContext.RootRunID, input.RunID, input.View)
	if err != nil {
		return nil, err
	}
	input = loadedPlanActivityInput(input, view)
	defer returnLoadedView(&out, view)
	if err := r.restoreRecoveryCorrection(ctx, input); err != nil {
		return nil, err
	}
	events := newPlannerEvents(r, input.AgentID, input.RunID, input.RunContext.SessionID, input.RunContext.TurnID)
	events.step = input.Step
	recoveryRecorder := &modelRecoveryRecorder{}
	recordedView := input.View
	recordedView.IndexHead, recordedView.Pending = view.Head, nil
	callRecording := NewCallRecording(r.RunStore, input.RunContext.RootRunID, recordedView, view.Messages, input.Step)
	callRecording.configureOverflow(input)
	defer callRecording.finishActivity(ctx, &out, &activityErr, events)
	reg, agentCtx, err := r.plannerContext(ctx, input, events, recoveryRecorder, callRecording)
	if err != nil {
		return nil, err
	}
	planInput, err := r.resumePlanInput(ctx, reg, agentCtx, events, input, view)
	if err != nil {
		return nil, err
	}
	result, err := r.planResume(ctx, reg, planInput)
	if out, handled, err := resolvePlanActivityOverflow(ctx, events, callRecording, recoveryRecorder, input); handled {
		return out, err
	}
	if out, handled, err := r.resolvePlanActivityRecovery(ctx, events, recoveryRecorder, input, err); handled {
		return out, err
	}
	return r.completePlanActivity(ctx, result, input, events, recoveryRecorder)
}

// loadedPlanActivityInput owns the adopted loader references for call recording.
func loadedPlanActivityInput(input *PlanActivityInput, view *loadedView) *PlanActivityInput {
	owned := *input
	owned.View.IndexHead, owned.View.Pending = view.Head, nil
	if view.View != nil {
		owned.View.Checkpoint = new(view.View.Checkpoint)
		owned.View.Pinned = view.View.Pinned
	}
	return &owned
}

// returnLoadedView excludes recoverable rejections from view adoption.
func returnLoadedView(out **PlanActivityOutput, view *loadedView) {
	if *out != nil && (*out).Recovery == nil {
		(*out).View = view.View
	}
}

func plannerActivityError(ctx context.Context, events *runtimePlannerEvents, err error) error {
	if errors.Is(err, model.ErrRateLimited) {
		events.PlannerThought(
			ctx,
			"Model provider is rate-limiting this request. It is safe to retry after a short delay.",
			map[string]string{plannerThoughtCodeKey: "rate_limited"},
		)
	}
	err = errors.Join(err, events.hookError())
	if err != nil {
		events.discardModelPresentations(ctx)
	}
	return err
}

func recoveryPlanActivityOutput(input *PlanActivityInput, recovery *ModelRecovery, usage model.TokenUsage) *PlanActivityOutput {
	return &PlanActivityOutput{
		Recovery:         recovery,
		IndexHead:        input.View.IndexHead,
		Usage:            usage,
		ToolPolicyActive: input.ToolPolicyActive,
		AllowedTools:     cloneToolIdents(input.AllowedTools),
		PolicyCaps:       input.PolicyCaps,
		Attempt:          input.RunContext.Attempt,
	}
}

// resolvePlanActivityOverflow discards the entire attempt even when its planner
// swallowed a model failure or returned a usable result from an earlier call.
func resolvePlanActivityOverflow(ctx context.Context, events *runtimePlannerEvents, calls *CallRecording, recorder *modelRecoveryRecorder, input *PlanActivityInput) (*PlanActivityOutput, bool, error) {
	failure := calls.Overflow()
	if failure == nil {
		return nil, false, nil
	}
	events.discardModelPresentations(ctx)
	if err := events.hookError(); err != nil {
		return nil, true, err
	}
	usage, err := recorder.activityUsage(events.exportUsage())
	if err != nil {
		return nil, true, err
	}
	return &PlanActivityOutput{ContextOverflow: failure, IndexHead: input.View.IndexHead, Usage: usage}, true, nil
}

func nonRetryableProviderError(err error) error {
	if errors.Is(err, model.ErrUnsupportedPart) {
		return engine.NewNonRetryableApplicationError(err.Error(), "model.unsupported_part", nil, err)
	}

	provider, ok := model.AsProviderError(err)
	if ok && !provider.Retryable() {
		return engine.NewNonRetryableApplicationError(err.Error(), "provider_error."+string(provider.Kind()), nil, err)
	}
	return err
}

// validatePlannerClarificationQuestions rejects blank prompts before committing
// presentations or storing any content from the planner result.
func validatePlannerClarificationQuestions(result *planner.PlanResult) error {
	if result.Await == nil {
		return nil
	}
	for i, item := range result.Await.Items {
		if item.Clarification != nil && strings.TrimSpace(item.Clarification.Question) == "" {
			return engine.NewNonRetryableApplicationError(
				fmt.Sprintf("planner await item %d is invalid: clarification question must be nonempty", i),
				PlannerContractErrorType, nil, nil,
			)
		}
	}
	return nil
}

func normalizeTranscriptRawJSON(messages []*model.Message) {
	for msgIdx := range messages {
		msg := messages[msgIdx]
		if msg == nil {
			continue
		}
		for partIdx, part := range msg.Parts {
			switch value := part.(type) {
			case model.ToolUsePart:
				value.Input = normalizeAnyRawMessage(value.Input)
				msg.Parts[partIdx] = value
			case model.ToolResultPart:
				value.Content = normalizeAnyRawMessage(value.Content)
				msg.Parts[partIdx] = value
			}
		}
		for key, value := range msg.Meta {
			msg.Meta[key] = normalizeAnyRawMessage(value)
		}
	}
}

func normalizeAnyRawMessage(value any) any {
	switch typed := value.(type) {
	case jsontext.Value:
		if len(bytes.TrimSpace(typed)) == 0 {
			return nil
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeAnyRawMessage(item)
		}
		return typed
	case []any:
		for idx, item := range typed {
			typed[idx] = normalizeAnyRawMessage(item)
		}
		return typed
	default:
		return value
	}
}

func (r *Runtime) startPlanInput(
	ctx context.Context,
	reg *AgentRegistration,
	agentCtx planner.PlannerContext,
	events planner.PlannerEvents,
	input *PlanActivityInput,
	view *loadedView,
) (*planner.PlanInput, error) {
	preloadedMemory, err := r.preloadMemory(ctx, reg.Policy.PreloadMemory, string(input.AgentID), input.RunContext, agentCtx.Memory())
	if err != nil {
		return nil, err
	}
	messages, err := r.applyHistoryPolicy(ctx, reg, view.Messages)
	if err != nil {
		return nil, err
	}
	messages = applyRecoveryInstruction(messages, input.Recovery)
	preloadedEntries, err := r.preloadLongTermMemory(ctx, reg.Policy.PreloadLongTermMemory, string(input.AgentID), input.RunContext, messages)
	if err != nil {
		return nil, err
	}
	return &planner.PlanInput{
		Messages:               messages,
		RunContext:             input.RunContext,
		Agent:                  agentCtx,
		Events:                 events,
		Reminders:              r.reminderSnapshot(input.RunID),
		PreloadedMemory:        preloadedMemory,
		PreloadedMemoryEntries: preloadedEntries,
	}, nil
}

func (r *Runtime) resolvePlanActivityRecovery(
	ctx context.Context,
	events *runtimePlannerEvents,
	recorder *modelRecoveryRecorder,
	input *PlanActivityInput,
	planErr error,
) (*PlanActivityOutput, bool, error) {
	planErr = plannerActivityError(ctx, events, planErr)
	recovery, recoveryErr := recorder.recovery(planErr, input.RunContext.Attempt)
	if recovery != nil {
		usage, err := recorder.activityUsage(events.exportUsage())
		if err != nil {
			return nil, true, err
		}
		out, err := r.storePlanActivityOutput(ctx, input.RunContext.RootRunID, recoveryPlanActivityOutput(input, recovery, usage), nil, nil)
		return out, true, err
	}
	if recoveryErr != nil {
		return nil, true, nonRetryableProviderError(recoveryErr)
	}
	if planErr != nil {
		return nil, true, nonRetryableProviderError(planErr)
	}
	return nil, false, nil
}

func (r *Runtime) completePlanActivity(
	ctx context.Context,
	result *planner.PlanResult,
	input *PlanActivityInput,
	events *runtimePlannerEvents,
	recorder *modelRecoveryRecorder,
) (*PlanActivityOutput, error) {
	if err := r.preparePlanActivityCalls(ctx, input, result); err != nil {
		events.discardModelPresentations(ctx)
		return nil, err
	}
	if err := events.hookError(); err != nil {
		events.discardModelPresentations(ctx)
		return nil, err
	}
	usage, err := recorder.activityUsage(events.exportUsage())
	if err != nil {
		events.discardModelPresentations(ctx)
		return nil, err
	}
	if err := events.commitModelPresentations(ctx); err != nil {
		return nil, err
	}
	if !result.Streamed && events.presentationOwnsFinalResponse(result) {
		result.Streamed = true
	}
	transcript := events.exportTranscript()
	if !result.Streamed && result.FinalResponse != nil {
		transcript = []*model.Message{finalPlannerMessage(&PlanActivityOutput{Result: result})}
	}
	normalizeTranscriptRawJSON(transcript)
	if result.Streamed && result.FinalResponse != nil && agentMessageText(result.FinalResponse.Message) == "" {
		if text := transcriptText(transcript); text != "" {
			result.FinalResponse.Message = newTextAgentMessage(model.ConversationRoleAssistant, text)
		}
	}
	return r.storePlanActivityOutput(ctx, input.RunContext.RootRunID, &PlanActivityOutput{
		Result:           result,
		IndexHead:        input.View.IndexHead,
		Usage:            usage,
		ToolPolicyActive: input.ToolPolicyActive,
		AllowedTools:     cloneToolIdents(input.AllowedTools),
		PolicyCaps:       input.PolicyCaps,
		Attempt:          input.RunContext.Attempt,
	}, transcript, canonicalAwaitCalls(result))
}

func (r *Runtime) canonicalizePlanActivityToolCalls(result *planner.PlanResult, input *PlanActivityInput) {
	if result == nil {
		return
	}
	calls := r.applyPerRunOverrides(context.Background(), &RunInput{Policy: &PolicyOverrides{RestrictToTool: input.CallFilter.RestrictToTool, AllowedTags: input.CallFilter.AllowedTags, DeniedTags: input.CallFilter.DeniedTags}}, result.ToolCalls)
	calls = r.rewriteUnknownToolCalls(calls, toolPolicyEnvelope{
		Active:     input.ToolPolicyActive,
		Allowed:    cloneToolIdents(input.AllowedTools),
		ExtraTools: input.ExtraTools,
	})
	base := &workflowPlanInput{RunContext: input.RunContext}
	parent := newChildTracker(input.RunContext.ParentToolCallID)
	result.ToolCalls = r.prepareAllowedCallsMetadata(input.AgentID, base, calls, parent)
	if input.Finalize != nil {
		for i := range result.ToolCalls {
			call := &result.ToolCalls[i]
			call.ToolCallID = generateDeterministicToolCallID(input.RunContext.RunID, input.RunContext.TurnID, input.RunContext.Attempt, call.Name, i)
			if call.Labels == nil {
				call.Labels = make(map[string]string)
			}
			call.Labels[FinalizationReasonLabel] = string(input.Finalize.Reason)
		}
	}
}

func (r *Runtime) resumePlanInput(
	ctx context.Context,
	reg *AgentRegistration,
	agentCtx planner.PlannerContext,
	events planner.PlannerEvents,
	input *PlanActivityInput,
	view *loadedView,
) (*planner.PlanResumeInput, error) {
	if input.Finalize != nil {
		view.Typed = nil
	} else {
		view.Messages = cloneMessages(view.Messages)
		if err := transcript.ValidateBedrock(view.Messages, false); err != nil {
			return nil, err
		}
	}
	preloadedMemory, err := r.preloadMemory(ctx, reg.Policy.PreloadMemory, string(input.AgentID), input.RunContext, agentCtx.Memory())
	if err != nil {
		return nil, err
	}
	messages, err := r.applyHistoryPolicy(ctx, reg, view.Messages)
	if err != nil {
		return nil, err
	}
	messages = applyRecoveryInstruction(messages, input.Recovery)
	preloadedEntries, err := r.preloadLongTermMemory(ctx, reg.Policy.PreloadLongTermMemory, string(input.AgentID), input.RunContext, messages)
	if err != nil {
		return nil, err
	}
	return &planner.PlanResumeInput{
		Messages:               messages,
		RunContext:             input.RunContext,
		Agent:                  agentCtx,
		Events:                 events,
		ToolOutputs:            view.Outputs,
		Content:                viewContent{store: r.RunStore, scope: input.RunContext.RootRunID},
		TypedInputs:            view.Typed,
		Finalize:               input.Finalize,
		Reminders:              r.reminderSnapshot(input.RunID),
		PreloadedMemory:        preloadedMemory,
		PreloadedMemoryEntries: preloadedEntries,
	}, nil
}

func (r *Runtime) reminderSnapshot(runID string) []reminder.Reminder {
	if r.reminders == nil {
		return nil
	}
	return r.reminders.Snapshot(runID)
}

// planStart invokes the planner's PlanStart method with tracing.
func (r *Runtime) planStart(ctx context.Context, reg *AgentRegistration, input *planner.PlanInput) (*planner.PlanResult, error) {
	if reg.Planner == nil {
		return nil, errors.New("planner not configured")
	}
	if input == nil {
		return nil, errors.New("plan input is required")
	}
	tracer := r.tracer
	if tracer == nil {
		tracer = telemetry.NoopTracer{}
	}
	ctx, span := tracer.Start(ctx, "planner.plan_start")
	defer span.End()
	start := time.Now()
	result, err := reg.Planner.PlanStart(ctx, input)
	r.recordPlannerAttempt(span, reg.ID, "start", input.RunContext, time.Since(start), err)
	return result, err
}

// planResume invokes the planner's PlanResume method with tracing.
func (r *Runtime) planResume(ctx context.Context, reg *AgentRegistration, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if reg.Planner == nil {
		return nil, errors.New("planner not configured")
	}
	if input == nil {
		return nil, errors.New("plan resume input is required")
	}
	tracer := r.tracer
	if tracer == nil {
		tracer = telemetry.NoopTracer{}
	}
	ctx, span := tracer.Start(ctx, "planner.plan_resume")
	defer span.End()
	start := time.Now()
	result, err := reg.Planner.PlanResume(ctx, input)
	r.recordPlannerAttempt(span, reg.ID, "resume", input.RunContext, time.Since(start), err)
	return result, err
}

// plannerContext constructs the agent registration and context needed for planner execution.
func (r *Runtime) plannerContext(ctx context.Context, input *PlanActivityInput, events planner.PlannerEvents, recovery *modelRecoveryRecorder, recording *CallRecording) (*AgentRegistration, planner.PlannerContext, error) {
	if input.AgentID == "" {
		return nil, nil, errors.New("agent id is required")
	}
	reg, ok := r.agentByID(input.AgentID)
	if !ok {
		return nil, nil, fmt.Errorf("agent %q is not registered", input.AgentID)
	}
	reader, err := r.memoryReader(ctx, string(input.AgentID), input.RunID)
	if err != nil {
		return nil, nil, err
	}
	agentCtx := newAgentContext(agentContextOptions{
		runtime:     r,
		agentID:     input.AgentID,
		runID:       input.RunID,
		rootRunID:   input.RunContext.RootRunID,
		attempt:     engine.ActivityAttemptFromContext(ctx),
		plannerStep: input.Step,
		memory:      reader,
		sessionID:   input.RunContext.SessionID,
		labels:      input.RunContext.Labels,
		turnID:      input.RunContext.TurnID,
		events:      events,
		cache:       reg.Policy.Cache,
		recovery:    recovery,
		recording:   recording,
		toolPolicy: toolPolicyEnvelope{
			Active:     input.ToolPolicyActive,
			Allowed:    cloneToolIdents(input.AllowedTools),
			ExtraTools: input.ExtraTools,
		},
	})
	return &reg, agentCtx, nil
}

// preparePlanActivityCalls validates clarification questions and completes policy
// and identity changes before presentation commit and storage.
func (r *Runtime) preparePlanActivityCalls(ctx context.Context, input *PlanActivityInput, result *planner.PlanResult) error {
	if err := validatePlannerClarificationQuestions(result); err != nil {
		return err
	}
	if input.Finalize != nil && len(result.ToolCalls) > 0 {
		calls, err := r.finalizationActivityCalls(ctx, input, result.ToolCalls)
		if err != nil {
			return err
		}
		result.ToolCalls = calls
	} else {
		r.canonicalizePlanActivityToolCalls(result, input)
	}
	return nil
}

// restoreRecoveryCorrection loads only the explicit output-validation correction.
func (r *Runtime) restoreRecoveryCorrection(ctx context.Context, input *PlanActivityInput) error {
	if input.Recovery != nil {
		copy := *input.Recovery
		data, err := r.RunStore.Get(ctx, input.RunContext.RootRunID, copy.CorrectionRef)
		if err != nil {
			return err
		}
		copy.Correction = string(data)
		if err := validateRecoveryCorrection(copy.Correction); err != nil {
			return err
		}
		input.Recovery = &copy
	}
	return nil
}
