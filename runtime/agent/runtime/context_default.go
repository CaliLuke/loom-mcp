package runtime

import (
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ContextDefault configures qualified compaction defaults for an agent.
type ContextDefault = api.ContextDefault

// Qualification self-certifies one exact provider configuration and model set.
type Qualification = compaction.Qualification

type defaultSettingsKey struct{}

// WithoutContextDefault disables implicit threshold compaction for an agent.
// An explicit WithContextCompaction remains authoritative.
func WithoutContextDefault(id agent.Ident) RuntimeOption {
	return func(opts *Options) {
		if id == "" {
			panic("runtime: context default requires an agent ID")
		}
		if opts.ContextDefault == nil {
			opts.ContextDefault = make(map[agent.Ident]ContextDefault)
		}
		opts.ContextDefault[id] = ContextDefault{Disabled: true}
	}
}

// WithContextQualification self-certifies an exact provider/model configuration.
// It retains default policy provenance and attachment residency protection.
func WithContextQualification(q Qualification) RuntimeOption {
	owned := cloneQualifications([]Qualification{q})[0]
	return func(opts *Options) {
		opts.ContextQualifications = append(opts.ContextQualifications, owned)
	}
}

func cloneQualifications(values []Qualification) []Qualification {
	out := slices.Clone(values)
	for i, q := range out {
		if q.Config == "" || len(q.Models) == 0 || (q.Tier != compaction.Evict && q.Tier != compaction.Summary) {
			panic("runtime: context qualification requires configuration, exact models and evict or summary tier")
		}
		for _, id := range q.Models {
			if id == "" {
				panic("runtime: qualification model must not be empty")
			}
		}
		out[i].Models = slices.Clone(q.Models)
	}
	return out
}

func cloneDefaultOptions(values map[agent.Ident]ContextDefault) map[agent.Ident]ContextDefault {
	if values == nil {
		return nil
	}
	out := make(map[agent.Ident]ContextDefault, len(values))
	for id, value := range values {
		if value.ContextModel != nil {
			value.ContextModel = new(*value.ContextModel)
		}
		out[id] = value
	}
	return out
}

func defaultContextSettings(settings compaction.Settings, id agent.Ident, defaults map[agent.Ident]ContextDefault) compaction.Settings {
	if settings.Threshold {
		return settings
	}
	value, ok := defaults[id]
	if !ok || value.Disabled {
		return settings
	}
	settings.Default = true
	settings.KeepNonTextResident = true
	if value.ContextModel != nil {
		settings.Summary.Enabled = true
		settings.Summary.Model = *value.ContextModel
	}
	return settings
}

// defaultRecallConflict checks the same reserved names without making defaults
// a registration error. The caller holds the registry read lock.
func (r *Runtime) defaultRecallConflict(reg AgentRegistration) bool {
	if validateRecallNames(reg.Specs) != nil {
		return true
	}
	for _, toolset := range reg.Toolsets {
		if validateRecallNames(toolset.Specs) != nil {
			return true
		}
	}
	if len(reg.Specs) == 0 {
		for _, spec := range r.toolSpecs {
			if validateRecallNames([]tools.ToolSpec{spec}) != nil {
				return true
			}
		}
	}
	return false
}
