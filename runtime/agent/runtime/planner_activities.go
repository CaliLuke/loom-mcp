package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/reminder"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
)

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
	var rems []reminder.Reminder
	if r.reminders != nil {
		rems = r.reminders.Snapshot(input.RunID)
	}
	msgs := r.applyHistoryPolicy(ctx, reg, input.Messages)
	planInput := &planner.PlanInput{
		Messages:   msgs,
		RunContext: input.RunContext,
		Agent:      agentCtx,
		Events:     events,
		Reminders:  rems,
	}
	result, err := r.planStart(ctx, reg, planInput)
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			events.PlannerThought(
				ctx,
				"Model provider is rate-limiting this request. It is safe to retry after a short delay.",
				map[string]string{"code": "rate_limited"},
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
		Result:     result,
		Transcript: transcript,
		Usage:      events.exportUsage(),
	}
	return out, nil
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
	var rems []reminder.Reminder
	if r.reminders != nil {
		rems = r.reminders.Snapshot(input.RunID)
	}
	msgs := r.applyHistoryPolicy(ctx, reg, input.Messages)
	planInput := &planner.PlanResumeInput{
		Messages:    msgs,
		RunContext:  input.RunContext,
		Agent:       agentCtx,
		Events:      events,
		ToolOutputs: toolOutputs,
		Finalize:    input.Finalize,
		Reminders:   rems,
	}
	result, err := r.planResume(ctx, reg, planInput)
	if err != nil {
		if errors.Is(err, model.ErrRateLimited) {
			events.PlannerThought(
				ctx,
				"Model provider is rate-limiting this request. It is safe to retry after a short delay.",
				map[string]string{"code": "rate_limited"},
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
		Result:     result,
		Transcript: transcript,
		Usage:      events.exportUsage(),
	}
	return out, nil
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
	return reg.Planner.PlanStart(ctx, input)
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
	return reg.Planner.PlanResume(ctx, input)
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
	case json.RawMessage:
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
