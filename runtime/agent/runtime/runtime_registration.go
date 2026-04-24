package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	rthints "github.com/CaliLuke/loom-mcp/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// Seal closes the registration phase and activates engines that stage worker handlers.
func (r *Runtime) Seal(ctx context.Context) error {
	alreadyClosed := r.closeRegistration()
	if alreadyClosed {
		return nil
	}
	if sealer, ok := r.Engine.(engine.RegistrationSealer); ok {
		return sealer.SealRegistration(ctx)
	}
	return nil
}

// RegisterAgent validates the registration, registers workflows and activities, and stores agent metadata.
func (r *Runtime) RegisterAgent(ctx context.Context, reg AgentRegistration) error {
	if err := r.ensureRegistrationOpen(); err != nil {
		return err
	}
	if err := r.validateAgentRegistration(reg); err != nil {
		return err
	}
	if err := r.ensureHookActivityRegistered(ctx); err != nil {
		return err
	}
	reg = r.applyAgentWorkerQueueOverrides(reg)
	reg = applyAgentActivityDefaults(reg)
	if err := r.registerAgentWithEngine(ctx, reg); err != nil {
		return err
	}
	return r.storeRegisteredAgent(reg)
}

func (r *Runtime) ensureHookActivityRegistered(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hookActivityRegistered {
		return nil
	}
	opts := r.hookActivityRegistrationOptions()
	if err := r.Engine.RegisterHookActivity(ctx, hookActivityName, opts, r.hookActivity); err != nil {
		return err
	}
	r.hookActivityRegistered = true
	return nil
}

// RegisterToolset registers a toolset outside of agent registration.
func (r *Runtime) RegisterToolset(ts ToolsetRegistration) error {
	if err := r.ensureRegistrationOpen(); err != nil {
		return err
	}
	if err := validateToolsetRegistration(ts); err != nil {
		return err
	}
	if err := validateAgentToolsetSpecs(ts); err != nil {
		return err
	}
	return r.storeRegisteredToolset(ts)
}

func (r *Runtime) ensureRegistrationOpen() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.registrationClosed {
		return ErrRegistrationClosed
	}
	return nil
}

func (r *Runtime) validateAgentRegistration(reg AgentRegistration) error {
	if reg.ID == "" {
		return fmt.Errorf("%w: missing agent ID", ErrInvalidConfig)
	}
	if reg.Planner == nil {
		return fmt.Errorf("%w: missing planner", ErrInvalidConfig)
	}
	if reg.Workflow.Handler == nil {
		return fmt.Errorf("%w: missing workflow handler", ErrInvalidConfig)
	}
	if reg.ExecuteToolActivity == "" {
		return fmt.Errorf("%w: missing execute tool activity name", ErrInvalidConfig)
	}
	if reg.PlanActivityName == "" {
		return fmt.Errorf("%w: missing plan activity name", ErrInvalidConfig)
	}
	if reg.ResumeActivityName == "" {
		return fmt.Errorf("%w: missing resume activity name", ErrInvalidConfig)
	}
	if r.Engine == nil {
		return ErrEngineNotConfigured
	}
	return nil
}

func (r *Runtime) applyAgentWorkerQueueOverrides(reg AgentRegistration) AgentRegistration {
	cfg, ok := r.workers[reg.ID]
	if !ok || cfg.Queue == "" {
		return reg
	}
	reg.Workflow.TaskQueue = cfg.Queue
	reg.PlanActivityOptions.Queue = cfg.Queue
	reg.ResumeActivityOptions.Queue = cfg.Queue
	reg.ExecuteToolActivityOptions.Queue = cfg.Queue
	return reg
}

// Dispatch modes for ToolsetRegistration. See DispatchMode for context.
const (
	// DispatchActivity runs tools as workflow activities (default for
	// service-backed toolsets: isolation, retries, per-queue placement).
	DispatchActivity DispatchMode = iota
	// DispatchInline runs tool Execute callbacks directly in the workflow loop.
	// Used for workflow-native toolsets that must share the workflow context.
	DispatchInline
	// DispatchAgentChild starts a nested agent as a child workflow and adapts
	// its RunOutput to a ToolResult. Used for agent-as-tool registrations.
	DispatchAgentChild
)

// resolveToolsetDispatchMode derives the DispatchMode for a registration from
// the existing Inline / AgentTool signals when DispatchMode is unset. Agent
// tool registrations always dispatch as AgentChild; other inline toolsets run
// as Inline; everything else runs as an activity.
func resolveToolsetDispatchMode(ts ToolsetRegistration) DispatchMode {
	if ts.DispatchMode != DispatchActivity {
		return ts.DispatchMode
	}
	if ts.AgentTool != nil {
		return DispatchAgentChild
	}
	if ts.Inline {
		return DispatchInline
	}
	return DispatchActivity
}

func applyAgentActivityDefaults(reg AgentRegistration) AgentRegistration {
	if reg.PlanActivityOptions.StartToCloseTimeout == 0 {
		reg.PlanActivityOptions.StartToCloseTimeout = defaultPlanActivityTimeout
	}
	if reg.ResumeActivityOptions.StartToCloseTimeout == 0 {
		reg.ResumeActivityOptions.StartToCloseTimeout = defaultResumeActivityTimeout
	}
	if reg.ExecuteToolActivityOptions.StartToCloseTimeout == 0 {
		reg.ExecuteToolActivityOptions.StartToCloseTimeout = defaultExecuteToolActivityTimeout
	}
	return reg
}

func (r *Runtime) registerAgentWithEngine(ctx context.Context, reg AgentRegistration) error {
	if err := r.Engine.RegisterWorkflow(ctx, reg.Workflow); err != nil {
		return err
	}
	if err := r.registerAgentActivities(ctx, reg); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) registerAgentActivities(ctx context.Context, reg AgentRegistration) error {
	if reg.PlanActivityName != "" {
		if err := r.Engine.RegisterPlannerActivity(ctx, reg.PlanActivityName, reg.PlanActivityOptions, r.PlanStartActivity); err != nil {
			return err
		}
	}
	if reg.ResumeActivityName != "" {
		if err := r.Engine.RegisterPlannerActivity(ctx, reg.ResumeActivityName, reg.ResumeActivityOptions, r.PlanResumeActivity); err != nil {
			return err
		}
	}
	if reg.ExecuteToolActivity != "" {
		if err := r.Engine.RegisterExecuteToolActivity(ctx, reg.ExecuteToolActivity, reg.ExecuteToolActivityOptions, r.ExecuteToolActivity); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) storeRegisteredAgent(reg AgentRegistration) error {
	toolsetErr := validateRegisteredAgentToolsets(reg.Toolsets)
	if toolsetErr != nil {
		return toolsetErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[reg.ID] = reg
	r.addToolSpecsLocked(reg.Specs)
	if len(reg.Specs) > 0 {
		r.agentToolSpecs[reg.ID] = cloneToolSpecs(reg.Specs)
	}
	for _, ts := range reg.Toolsets {
		r.addToolsetLocked(ts)
	}
	return nil
}

func validateRegisteredAgentToolsets(toolsets []ToolsetRegistration) error {
	for _, ts := range toolsets {
		if err := validateAgentToolsetSpecs(ts); err != nil {
			return err
		}
	}
	return nil
}

func validateToolsetRegistration(ts ToolsetRegistration) error {
	if ts.Name == "" {
		return errors.New("toolset name is required")
	}
	if ts.Execute == nil {
		return errors.New("toolset execute function is required")
	}
	return nil
}

func (r *Runtime) storeRegisteredToolset(ts ToolsetRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addToolsetLocked(ts)
	registerToolsetHints(ts)
	return nil
}

func (r *Runtime) closeRegistration() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	alreadyClosed := r.registrationClosed
	r.registrationClosed = true
	return alreadyClosed
}

func (r *Runtime) hookActivityRegistrationOptions() engine.ActivityOptions {
	timeout := defaultHookActivityTimeout
	if r.hookActivityTimeout > 0 {
		timeout = r.hookActivityTimeout
	}
	return engine.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy:         defaultRetriedActivityPolicy(),
	}
}

func cloneToolSpecs(specs []tools.ToolSpec) []tools.ToolSpec {
	cp := make([]tools.ToolSpec, len(specs))
	copy(cp, specs)
	return cp
}

func registerToolsetHints(ts ToolsetRegistration) {
	if len(ts.CallHints) > 0 {
		rthints.RegisterCallHints(ts.CallHints)
	}
	if len(ts.ResultHints) > 0 {
		rthints.RegisterResultHints(ts.ResultHints)
	}
}

func validateAgentToolsetSpecs(ts ToolsetRegistration) error {
	if ts.AgentTool == nil {
		return nil
	}
	if len(ts.Specs) == 0 {
		agentID := ""
		if ts.AgentTool != nil {
			agentID = string(ts.AgentTool.AgentID)
		}
		if agentID != "" {
			return fmt.Errorf("%w: agent toolset %q (agent=%s) requires tool specs/codecs", ErrInvalidConfig, ts.Name, agentID)
		}
		return fmt.Errorf("%w: agent toolset %q requires tool specs/codecs", ErrInvalidConfig, ts.Name)
	}
	return nil
}
