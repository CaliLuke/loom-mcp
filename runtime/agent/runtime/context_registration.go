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
		specs := reg.Specs
		if len(reg.Specs) == 0 {
			specs = make([]tools.ToolSpec, 0, len(r.toolSpecs))
			for _, spec := range r.toolSpecs {
				specs = append(specs, spec)
			}
		}
		owned.KeepResultTools = appendKeptSpecs(owned.KeepResultTools, specs)
		for _, ts := range reg.Toolsets {
			owned.KeepResultTools = appendKeptSpecs(owned.KeepResultTools, ts.Specs)
		}
		slices.Sort(owned.KeepResultTools)
		owned.KeepResultTools = slices.Compact(owned.KeepResultTools)
		reg.Policy.ContextCompaction = &owned
	}
	return reg, nil
}

// storeAgentContextLocked publishes the resolved settings with the agent.
func (r *Runtime) storeAgentContextLocked(reg AgentRegistration) {
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
