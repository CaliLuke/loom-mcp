package runtime

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/reminder"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
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
func (r *Runtime) PlanStartActivity(ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	events := newPlannerEvents(r, input.AgentID, input.RunID, input.RunContext.SessionID, input.RunContext.TurnID)
	reg, agentCtx, err := r.plannerContext(ctx, input, events)
	if err != nil {
		return nil, err
	}
	planInput, err := r.startPlanInput(ctx, reg, agentCtx, events, input)
	if err != nil {
		return nil, err
	}
	result, err := r.planStart(ctx, reg, planInput)
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			events.PlannerThought(
				ctx,
				"Model provider is rate-limiting this request. It is safe to retry after a short delay.",
				map[string]string{plannerThoughtCodeKey: "rate_limited"},
			)
		}
		return nil, err
	}
	r.logger.Info(ctx, "PlanStartActivity returning PlanResult", "tool_calls", len(result.ToolCalls), "final_response", result.FinalResponse != nil, "await", result.Await != nil)
	if err := events.hookError(); err != nil {
		return nil, err
	}
	transcript := events.exportTranscript()
	normalizeTranscriptRawJSON(transcript)
	out := &PlanActivityOutput{
		Result:           result,
		Transcript:       transcript,
		Usage:            events.exportUsage(),
		ToolPolicyActive: input.ToolPolicyActive,
		AllowedTools:     cloneToolIdents(input.AllowedTools),
		PolicyCaps:       input.PolicyCaps,
	}
	return out, nil
}

func (r *Runtime) startPlanInput(
	ctx context.Context,
	reg *AgentRegistration,
	agentCtx planner.PlannerContext,
	events planner.PlannerEvents,
	input *PlanActivityInput,
) (*planner.PlanInput, error) {
	preloadedMemory, err := r.preloadMemory(ctx, reg.Policy.PreloadMemory, string(input.AgentID), input.RunContext, agentCtx.Memory())
	if err != nil {
		return nil, err
	}
	messages, err := r.applyHistoryPolicy(ctx, reg, input.Messages)
	if err != nil {
		return nil, err
	}
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
func (r *Runtime) PlanResumeActivity(ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	events := newPlannerEvents(r, input.AgentID, input.RunID, input.RunContext.SessionID, input.RunContext.TurnID)
	reg, agentCtx, err := r.plannerContext(ctx, input, events)
	if err != nil {
		return nil, err
	}
	toolOutputs, err := r.decodeToolOutputs(input.ToolOutputs)
	if err != nil {
		return nil, err
	}
	planInput, err := r.resumePlanInput(ctx, reg, agentCtx, events, input, toolOutputs)
	if err != nil {
		return nil, err
	}
	result, err := r.planResume(ctx, reg, planInput)
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			events.PlannerThought(
				ctx,
				"Model provider is rate-limiting this request. It is safe to retry after a short delay.",
				map[string]string{plannerThoughtCodeKey: "rate_limited"},
			)
		}
		return nil, err
	}
	if err := events.hookError(); err != nil {
		return nil, err
	}
	transcript := events.exportTranscript()
	normalizeTranscriptRawJSON(transcript)
	out := &PlanActivityOutput{
		Result:           result,
		Transcript:       transcript,
		Usage:            events.exportUsage(),
		ToolPolicyActive: input.ToolPolicyActive,
		AllowedTools:     cloneToolIdents(input.AllowedTools),
		PolicyCaps:       input.PolicyCaps,
	}
	return out, nil
}

func (r *Runtime) resumePlanInput(
	ctx context.Context,
	reg *AgentRegistration,
	agentCtx planner.PlannerContext,
	events planner.PlannerEvents,
	input *PlanActivityInput,
	toolOutputs []*planner.ToolOutput,
) (*planner.PlanResumeInput, error) {
	preloadedMemory, err := r.preloadMemory(ctx, reg.Policy.PreloadMemory, string(input.AgentID), input.RunContext, agentCtx.Memory())
	if err != nil {
		return nil, err
	}
	messages, err := r.applyHistoryPolicy(ctx, reg, input.Messages)
	if err != nil {
		return nil, err
	}
	preloadedEntries, err := r.preloadLongTermMemory(ctx, reg.Policy.PreloadLongTermMemory, string(input.AgentID), input.RunContext, messages)
	if err != nil {
		return nil, err
	}
	return &planner.PlanResumeInput{
		Messages:               messages,
		RunContext:             input.RunContext,
		Agent:                  agentCtx,
		Events:                 events,
		ToolOutputs:            toolOutputs,
		TypedInputs:            cloneTypedInputOutputs(input.TypedInputs),
		Finalize:               input.Finalize,
		Reminders:              r.reminderSnapshot(input.RunID),
		PreloadedMemory:        preloadedMemory,
		PreloadedMemoryEntries: preloadedEntries,
	}, nil
}

func cloneTypedInputOutputs(in []planner.TypedInputOutput) []planner.TypedInputOutput {
	if len(in) == 0 {
		return nil
	}
	out := make([]planner.TypedInputOutput, len(in))
	for i, item := range in {
		out[i] = planner.TypedInputOutput{
			ID:      item.ID,
			Payload: append(item.Payload[:0:0], item.Payload...),
		}
	}
	return out
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
func (r *Runtime) plannerContext(ctx context.Context, input *PlanActivityInput, events planner.PlannerEvents) (*AgentRegistration, planner.PlannerContext, error) {
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
		runtime:   r,
		agentID:   input.AgentID,
		runID:     input.RunID,
		memory:    reader,
		sessionID: input.RunContext.SessionID,
		labels:    input.RunContext.Labels,
		turnID:    input.RunContext.TurnID,
		events:    events,
		cache:     reg.Policy.Cache,
		toolPolicy: toolPolicyEnvelope{
			Active:  input.ToolPolicyActive,
			Allowed: cloneToolIdents(input.AllowedTools),
		},
	})
	return &reg, agentCtx, nil
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
