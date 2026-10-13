package runtime

import (
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func appendKeptSpecs(kept []tools.Ident, specs []tools.ToolSpec) []tools.Ident {
	for _, spec := range specs {
		if spec.KeepResultInContext {
			kept = append(kept, spec.Name)
		}
	}
	return kept
}

func registrationSummary(reg AgentRegistration) SummarySettings {
	if reg.Policy.ContextCompaction == nil {
		return SummarySettings{}
	}
	return reg.Policy.ContextCompaction.Summary
}

// initContextOptions keeps the original options separate from settings resolved
// during agent registration, so re-registration cannot create option conflicts.
func (r *Runtime) initContextOptions(opts Options) {
	r.contextDefault = opts.ContextDefault
	r.contextQualifications = opts.ContextQualifications
	r.agentContextDefault = cloneDefaultOptions(opts.ContextDefault)
	r.contextCompaction = opts.ContextCompaction
	r.contextRecovery = opts.ContextRecovery
	r.agentContextCompaction = cloneCompactionOptions(opts.ContextCompaction)
	r.agentContextRecovery = cloneRecoveryOptions(opts.ContextRecovery)
}

// resolveAgentContext owns registration policy and uses the runtime-option
// normalizers for both sources. It does not publish settings before validation.
func (r *Runtime) resolveAgentContext(reg AgentRegistration) (AgentRegistration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	option, hasOption := r.contextCompaction[reg.ID]
	settings := reg.Policy.ContextCompaction
	if settings != nil && hasOption {
		return reg, fmt.Errorf("%w: agent %q: RunPolicy.ContextCompaction and WithContextCompaction cannot both be configured", ErrInvalidConfig, reg.ID)
	}
	if hasOption {
		settings = &option
	}
	recovery, hasRecovery := r.contextRecovery[reg.ID]
	if reg.Policy.ContextRecovery != nil && hasRecovery {
		return reg, fmt.Errorf("%w: agent %q: RunPolicy.ContextRecovery and WithContextRecovery cannot both be configured", ErrInvalidConfig, reg.ID)
	}
	if reg.Policy.ContextRecovery != nil {
		recovery = *reg.Policy.ContextRecovery
		hasRecovery = true
	}
	if hasRecovery {
		owned := cloneRecoveryOptions(map[agent.Ident]ContextRecovery{reg.ID: recovery})[reg.ID]
		reg.Policy.ContextRecovery = &owned
	}
	if settings != nil {
		owned, err := normalizeCompaction(reg.ID, *settings)
		if err != nil {
			return reg, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
		}
		owned.KeepResultTools = r.registeredKeptTools(reg, owned.KeepResultTools)
		reg.Policy.ContextCompaction = &owned
	}
	reg = r.resolveAgentDefault(reg, settings == nil)
	return reg, nil
}

// storeAgentContextLocked publishes the resolved settings with the agent.
func (r *Runtime) storeAgentContextLocked(reg AgentRegistration) {
	if r.agentContextDefault == nil {
		r.agentContextDefault = make(map[agent.Ident]ContextDefault)
	}
	delete(r.agentContextDefault, reg.ID)
	if reg.Policy.ContextDefault != nil && reg.Planner != nil {
		r.agentContextDefault[reg.ID] = cloneDefaultOptions(map[agent.Ident]ContextDefault{reg.ID: *reg.Policy.ContextDefault})[reg.ID]
	}
	if reg.Policy.ContextCompaction != nil {
		if r.agentContextCompaction == nil {
			r.agentContextCompaction = make(map[agent.Ident]ContextCompaction)
		}
		r.agentContextCompaction[reg.ID] = *cloneCompaction(reg.Policy.ContextCompaction)
	}
	if reg.Policy.ContextRecovery != nil {
		if r.agentContextRecovery == nil {
			r.agentContextRecovery = make(map[agent.Ident]ContextRecovery)
		}
		r.agentContextRecovery[reg.ID] = *reg.Policy.ContextRecovery
	}
}

func (r *Runtime) resolveAgentDefault(reg AgentRegistration, enabled bool) AgentRegistration {
	if reg.Planner != nil {
		value := ContextDefault{}
		if reg.Policy.ContextDefault != nil {
			value = *reg.Policy.ContextDefault
		}
		if option, ok := r.contextDefault[reg.ID]; ok {
			value = option
		}
		if value.ContextModel != nil {
			value.ContextModel = new(*value.ContextModel)
		}
		if enabled && r.defaultRecallConflict(reg) {
			value.Disabled = true
		}
		reg.Policy.ContextDefault = &value
	}
	return reg
}

func (r *Runtime) registeredKeptTools(reg AgentRegistration, kept []tools.Ident) []tools.Ident {
	specs := reg.Specs
	if len(reg.Specs) == 0 {
		specs = make([]tools.ToolSpec, 0, len(r.toolSpecs))
		for _, spec := range r.toolSpecs {
			specs = append(specs, spec)
		}
	}
	kept = appendKeptSpecs(kept, specs)
	for _, ts := range reg.Toolsets {
		kept = appendKeptSpecs(kept, ts.Specs)
	}
	slices.Sort(kept)
	kept = slices.Compact(kept)
	return kept
}
