package runtime

// workflow_plan_activity.go contains plan-activity execution helpers used by the loop.
//
// Contract:
// - These helpers are deterministic and replay-safe: timeouts use workflow time.
// - Callers should only invoke them from within workflow execution.
// - The helpers publish lifecycle events via hooks so streams can close deterministically.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const maxRecoveryToolCatalogEntries = model.MaxToolDefinitionsPerRequest

var errRecoveryTurnCapExceeded = errors.New("recovery turn cap exceeded")

// runPlanActivity schedules a plan/resume activity with the configured options.
func (r *Runtime) runPlanActivity(
	wfCtx engine.WorkflowContext,
	activityName string,
	options engine.ActivityOptions,
	input PlanActivityInput,
	hardDeadline time.Time,
) (*PlanActivityOutput, error) {
	if activityName == "" {
		return nil, errors.New("plan activity not registered")
	}
	callOpts := capPlanActivityOptions(wfCtx, options, hardDeadline)
	out, err := wfCtx.ExecutePlannerActivity(wfCtx.Context(), engine.PlannerActivityCall{
		Name:    activityName,
		Input:   &input,
		Options: callOpts,
	})
	if err != nil {
		return nil, err
	}
	if err := validatePlanActivityOutput(out, input); err != nil {
		return nil, err
	}
	// Workflow input owns policy, cap, and attempt state. Activity output carries
	// compatibility echoes only; normalize them before any workflow consumes them.
	out.ToolPolicyActive = input.ToolPolicyActive
	out.AllowedTools = cloneToolIdents(input.AllowedTools)
	out.PolicyCaps = input.PolicyCaps
	out.Attempt = input.RunContext.Attempt
	r.logPlanActivityResult(wfCtx.Context(), out)
	return out, nil
}

func capPlanActivityOptions(wfCtx engine.WorkflowContext, options engine.ActivityOptions, hardDeadline time.Time) engine.ActivityOptions {
	callOpts := options
	startToClose := options.StartToCloseTimeout
	scheduleToStart := options.ScheduleToStartTimeout
	if !hardDeadline.IsZero() {
		if rem := hardDeadline.Sub(wfCtx.Now()); rem > 0 {
			if startToClose == 0 || startToClose > rem {
				startToClose = rem
			}
			if scheduleToStart == 0 || scheduleToStart > rem {
				scheduleToStart = rem
			}
		}
	}
	callOpts.StartToCloseTimeout = startToClose
	callOpts.ScheduleToStartTimeout = scheduleToStart
	return callOpts
}

func validatePlanActivityOutput(out *PlanActivityOutput, input PlanActivityInput) error {
	if out == nil {
		return fmt.Errorf("runPlanActivity received nil PlanActivityOutput")
	}
	if out.Result == nil && out.Recovery == nil {
		return fmt.Errorf("runPlanActivity received nil PlanResult")
	}
	if out.Result != nil && out.Recovery != nil {
		return fmt.Errorf("runPlanActivity received both PlanResult and Recovery")
	}
	if err := model.ValidateTokenUsage(out.Usage); err != nil {
		return fmt.Errorf("runPlanActivity received invalid Usage: %w", err)
	}
	if out.Recovery != nil {
		if err := validateModelRecovery(out.Recovery, input); err != nil {
			return fmt.Errorf("runPlanActivity received invalid Recovery: %w", err)
		}
		return nil
	}
	if len(out.Result.ToolCalls) == 0 &&
		out.Result.FinalResponse == nil &&
		out.Result.FinalToolResult == nil &&
		out.Result.Await == nil {
		return fmt.Errorf("runPlanActivity received PlanResult with no ToolCalls, FinalResponse, FinalToolResult, or Await")
	}
	if err := validatePlannerRecoveryCatalog(out.Result, input); err != nil {
		return err
	}
	return nil
}

func validateModelRecovery(recovery *ModelRecovery, input PlanActivityInput) error {
	if recovery == nil {
		return errors.New("recovery is required")
	}
	if strings.TrimSpace(recovery.Correction) == "" || len(recovery.Correction) > maxRecoveryCorrectionBytes || !utf8.ValidString(recovery.Correction) {
		return errors.New("correction must be non-empty, valid UTF-8, and within the recovery bound")
	}
	if recovery.ByteCount < 0 {
		return errors.New("byte count must be non-negative")
	}
	if recovery.Attempt != input.RunContext.Attempt {
		return fmt.Errorf("attempt %d does not match activity attempt %d", recovery.Attempt, input.RunContext.Attempt)
	}
	if err := model.ValidateTokenUsage(recovery.Usage); err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	switch recovery.Kind {
	case model.OutputValidationToolIdentity, model.OutputValidationToolArguments:
		if recovery.DisableTools {
			return errors.New("tool-call recovery must keep the rejected request catalog")
		}
		if err := validateRecoveryToolCatalog(recovery.ToolCatalog, input); err != nil {
			return err
		}
	case model.OutputValidationOutputBounds, model.OutputValidationStructuredOutput:
		if !recovery.DisableTools {
			return errors.New("final-answer recovery must disable tools")
		}
		if len(recovery.ToolCatalog) > 0 {
			return errors.New("final-answer recovery must not persist a tool catalog")
		}
	case model.OutputValidationResponseShape,
		model.OutputValidationToolChoice,
		model.OutputValidationStreamProtocol,
		model.OutputValidationUsage:
		return fmt.Errorf("unsupported recovery kind %q", recovery.Kind)
	default:
		return fmt.Errorf("unsupported recovery kind %q", recovery.Kind)
	}
	return nil
}

func validateRecoveryToolCatalog(catalog []tools.Ident, input PlanActivityInput) error {
	if catalog == nil {
		return errors.New("tool-call recovery requires the exact rejected request catalog")
	}
	if len(catalog) > maxRecoveryToolCatalogEntries {
		return fmt.Errorf("tool-call recovery catalog has %d entries; limit is %d", len(catalog), maxRecoveryToolCatalogEntries)
	}
	allowed := make(map[tools.Ident]struct{}, len(input.AllowedTools)+1)
	for _, name := range input.AllowedTools {
		allowed[name] = struct{}{}
	}
	allowed[tools.ToolUnavailable] = struct{}{}
	seen := make(map[tools.Ident]struct{}, len(catalog))
	for _, name := range catalog {
		if name == "" {
			return errors.New("tool-call recovery catalog contains an empty tool name")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("tool-call recovery catalog contains duplicate tool %q", name)
		}
		seen[name] = struct{}{}
		if input.ToolPolicyActive {
			if _, ok := allowed[name]; !ok {
				return fmt.Errorf("tool-call recovery catalog widens the policy with tool %q", name)
			}
		}
	}
	return nil
}

func validatePlannerRecoveryCatalog(result *planner.PlanResult, input PlanActivityInput) error {
	if result == nil || len(result.ToolCalls) == 0 {
		return nil
	}
	if input.Recovery != nil && input.Recovery.DisableTools {
		return errors.New("replacement final-answer turn returned tool calls while tools were disabled")
	}
	if input.Recovery == nil && !input.ToolPolicyActive {
		return nil
	}
	catalog := input.AllowedTools
	exactRecoveryCatalog := input.Recovery != nil && input.Recovery.ToolCatalog != nil
	if exactRecoveryCatalog {
		catalog = input.Recovery.ToolCatalog
	}
	allowed := make(map[tools.Ident]struct{}, len(catalog))
	for _, name := range catalog {
		allowed[name] = struct{}{}
	}
	if !exactRecoveryCatalog && len(catalog) > 0 {
		allowed[tools.ToolUnavailable] = struct{}{}
	}
	for _, call := range result.ToolCalls {
		if _, ok := allowed[call.Name]; !ok {
			return fmt.Errorf("planner returned tool %q outside the current recovery catalog", call.Name)
		}
	}
	return nil
}

func (r *Runtime) runPlanActivityRecovering(
	wfCtx engine.WorkflowContext,
	activityName string,
	options engine.ActivityOptions,
	input PlanActivityInput,
	hardDeadline time.Time,
	caps *policy.CapsState,
	nextAttempt *int,
) (*PlanActivityOutput, error) {
	var recoveryUsage model.TokenUsage
	for {
		out, err := r.runPlanActivity(wfCtx, activityName, options, input, hardDeadline)
		if err != nil {
			return nil, err
		}
		recoveryUsage, err = checkedAddTokenUsage(recoveryUsage, out.Usage)
		if err != nil {
			return nil, fmt.Errorf("aggregate model recovery usage: %w", err)
		}
		if out.Recovery == nil {
			out.Usage = recoveryUsage
			return out, nil
		}
		if !consumeRecoveryTurn(caps) {
			out.Usage = recoveryUsage
			return out, errRecoveryTurnCapExceeded
		}
		input.Recovery = out.Recovery
		input.PolicyCaps = *caps
		input.RunContext.Attempt++
		if nextAttempt != nil {
			*nextAttempt = input.RunContext.Attempt + 1
		}
		if out.Recovery.ToolCatalog != nil {
			input.ToolPolicyActive = true
			input.AllowedTools = cloneToolIdents(out.Recovery.ToolCatalog)
		}
		if out.Recovery.DisableTools {
			input.ToolPolicyActive = true
			input.AllowedTools = nil
		}
		if err := enforcePlanActivityInputBudget(input); err != nil {
			return nil, err
		}
	}
}

func (r *Runtime) logPlanActivityResult(ctx context.Context, out *PlanActivityOutput) {
	if out == nil {
		return
	}
	if out.Recovery != nil {
		r.logger.Info(ctx,
			"runPlanActivity received recoverable model rejection",
			"kind", out.Recovery.Kind,
			"attempt", out.Recovery.Attempt,
			"byte_count", out.Recovery.ByteCount,
			"disable_tools", out.Recovery.DisableTools,
		)
		return
	}
	if out.Result == nil {
		return
	}
	r.logger.Info(ctx,
		"runPlanActivity received PlanResult",
		"tool_calls",
		len(out.Result.ToolCalls),
		"final_response",
		out.Result.FinalResponse != nil,
		"final_tool_result",
		out.Result.FinalToolResult != nil,
		"await",
		out.Result.Await != nil,
	)
}

func (r *Runtime) publishPlannerNotes(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string, notes []planner.PlannerAnnotation) error {
	for _, note := range notes {
		if err := r.publishHook(ctx, hooks.NewPlannerNoteEvent(
			base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, note.Text, note.Labels,
		), turnID); err != nil {
			return err
		}
	}
	return nil
}
