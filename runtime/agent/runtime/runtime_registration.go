package runtime

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	rthints "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Seal closes the registration phase and activates engines that stage worker
// handlers until the runtime is fully configured. Worker deployments should
// call Seal after registering all toolsets and agents, before serving traffic.
// When the engine supports staged workers, Seal returns only after activation
// succeeds or ctx ends.
//
// Successful Seal calls are idempotent. The first call closes registration so
// later RegisterAgent/RegisterToolset/RegisterModel calls fail fast even if
// activation later fails. Callers may retry Seal after a context-limited
// activation failure.
func (r *Runtime) Seal(ctx context.Context) error {
	r.mu.Lock()
	alreadyActivated := r.activationComplete
	r.registrationClosed = true
	r.mu.Unlock()
	if alreadyActivated {
		return nil
	}

	r.sealMu.Lock()
	defer r.sealMu.Unlock()

	r.mu.Lock()
	if r.activationComplete {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	if sealer, ok := r.Engine.(engine.RegistrationSealer); ok {
		if err := sealer.SealRegistration(ctx); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.activationComplete = true
	r.mu.Unlock()
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
	resolved, err := r.resolveNamedAgentInterceptors(reg)
	if err != nil {
		return err
	}
	reg = resolved
	reg.mergedInterceptors = mergeAgentInterceptors(r.interceptors, reg.Interceptors)
	if err := r.registerAgentWithEngine(ctx, reg); err != nil {
		return err
	}
	return r.storeRegisteredAgent(reg)
}

func (r *Runtime) resolveNamedAgentInterceptors(reg AgentRegistration) (AgentRegistration, error) {
	if len(reg.Policy.NamedInterceptors) == 0 {
		return reg, nil
	}
	resolved := make([]Interceptor, 0, len(reg.Policy.NamedInterceptors)+len(reg.Interceptors))
	for _, id := range reg.Policy.NamedInterceptors {
		interceptor := r.namedInterceptors[id]
		if interceptor == nil {
			return reg, fmt.Errorf("%w: interceptor %q is not registered", ErrInvalidConfig, id)
		}
		resolved = append(resolved, interceptor)
	}
	reg.Interceptors = append(resolved, reg.Interceptors...)
	return reg, nil
}

func mergeAgentInterceptors(runtimeInterceptors, agentInterceptors []Interceptor) []Interceptor {
	if len(runtimeInterceptors) == 0 && len(agentInterceptors) == 0 {
		return nil
	}
	merged := make([]Interceptor, 0, len(runtimeInterceptors)+len(agentInterceptors))
	merged = append(merged, runtimeInterceptors...)
	merged = append(merged, agentInterceptors...)
	return merged
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
	if err := r.validateMemoryPreloadPolicy(reg); err != nil {
		return err
	}
	if err := r.validateLongTermMemoryPreloadPolicy(reg); err != nil {
		return err
	}
	if err := r.validateRegisteredAgentToolsets(reg.Toolsets); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) validateMemoryPreloadPolicy(reg AgentRegistration) error {
	policy := reg.Policy.PreloadMemory
	if policy == nil {
		return nil
	}
	switch policy.Scope {
	case MemoryScopeCurrentRun:
		if r.Memory == nil {
			return fmt.Errorf("%w: agent %q PreloadMemory scope %q requires runtime.WithMemoryStore", ErrInvalidConfig, reg.ID, policy.Scope)
		}
	case MemoryScopeIndexed:
		if r.MemorySearcher == nil {
			return fmt.Errorf("%w: agent %q PreloadMemory scope %q requires runtime.WithMemorySearcher", ErrInvalidConfig, reg.ID, policy.Scope)
		}
	case "":
		return fmt.Errorf("%w: agent %q PreloadMemory requires a scope", ErrInvalidConfig, reg.ID)
	default:
		return fmt.Errorf("%w: agent %q has unknown PreloadMemory scope %q", ErrInvalidConfig, reg.ID, policy.Scope)
	}
	return nil
}

func (r *Runtime) validateLongTermMemoryPreloadPolicy(reg AgentRegistration) error {
	if reg.Policy.PreloadLongTermMemory == nil {
		return nil
	}
	if r.MemoryService == nil {
		return fmt.Errorf("%w: agent %q PreloadLongTermMemory requires runtime.WithMemoryService", ErrInvalidConfig, reg.ID)
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
	// DispatchUnset preserves backwards compatibility for registrations that
	// still rely on Inline / AgentTool being inferred during registration.
	DispatchUnset DispatchMode = iota
	// DispatchActivity runs tools as workflow activities (default for
	// service-backed toolsets: isolation, retries, per-queue placement).
	DispatchActivity
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
	if ts.DispatchMode != DispatchUnset {
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
	reg.Specs = cloneToolSpecs(reg.Specs)
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

func (r *Runtime) validateRegisteredAgentToolsets(toolsets []ToolsetRegistration) error {
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
	if len(specs) == 0 {
		return nil
	}
	cp := make([]tools.ToolSpec, len(specs))
	for i := range specs {
		cp[i] = cloneToolSpec(specs[i])
	}
	return cp
}

func cloneToolSpec(spec tools.ToolSpec) tools.ToolSpec {
	out := spec
	out.Tags = append([]string(nil), spec.Tags...)
	if len(spec.Meta) > 0 {
		out.Meta = make(map[string][]string, len(spec.Meta))
		for key, values := range spec.Meta {
			out.Meta[key] = append([]string(nil), values...)
		}
	}
	if spec.Bounds != nil {
		bounds := *spec.Bounds
		if spec.Bounds.Paging != nil {
			paging := *spec.Bounds.Paging
			bounds.Paging = &paging
		}
		out.Bounds = &bounds
	}
	if len(spec.ServerData) > 0 {
		out.ServerData = make([]*tools.ServerDataSpec, len(spec.ServerData))
		for i, serverData := range spec.ServerData {
			if serverData == nil {
				continue
			}
			cloned := *serverData
			cloned.Type = cloneToolTypeSpec(serverData.Type)
			out.ServerData[i] = &cloned
		}
	}
	if spec.Confirmation != nil {
		confirmation := *spec.Confirmation
		out.Confirmation = &confirmation
	}
	out.Payload = cloneToolTypeSpec(spec.Payload)
	out.Result = cloneToolTypeSpec(spec.Result)
	return out
}

func cloneToolTypeSpec(spec tools.TypeSpec) tools.TypeSpec {
	out := spec
	out.Schema = append([]byte(nil), spec.Schema...)
	out.ExampleJSON = append([]byte(nil), spec.ExampleJSON...)
	out.ExampleInput = cloneToolJSONMap(spec.ExampleInput)
	return out
}

func cloneToolJSONMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneToolJSONValue(item)
	}
	return out
}

func cloneToolJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneToolJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneToolJSONValue(item)
		}
		return out
	case jsontext.Value:
		return append(jsontext.Value(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return typed
	}
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
	if err := validateAgentToolsetRoute(ts); err != nil {
		return err
	}
	return nil
}

func validateAgentToolsetRoute(ts ToolsetRegistration) error {
	route := ts.AgentTool.Route
	var missing []string
	if route.ID == "" {
		missing = append(missing, "agent id")
	}
	if route.WorkflowName == "" {
		missing = append(missing, "workflow name")
	}
	if route.DefaultTaskQueue == "" {
		missing = append(missing, "default task queue")
	}
	if len(missing) == 0 {
		return nil
	}
	agentID := ts.AgentTool.AgentID
	if agentID == "" {
		agentID = route.ID
	}
	tool := unknownID
	if len(ts.Specs) > 0 {
		tool = string(ts.Specs[0].Name)
	}
	return fmt.Errorf(
		"%w: agent toolset %q tool %q (agent=%s) has incomplete route: missing %s",
		ErrInvalidConfig,
		ts.Name,
		tool,
		agentID,
		strings.Join(missing, ", "),
	)
}
