package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type (
	// ContextCompaction configures opt-in context compaction for one agent.
	ContextCompaction         = api.ContextCompaction
	compactionSettingsKey     struct{}
	compactionWorkflowContext struct {
		engine.WorkflowContext
		settings map[agent.Ident]ContextCompaction
		recovery map[agent.Ident]ContextRecovery
	}
)

// WithContextCompaction enables threshold compaction. HistoryPolicy cannot be used with it.
func WithContextCompaction(id agent.Ident, settings ContextCompaction) RuntimeOption {
	return func(opts *Options) {
		if opts.ContextCompaction == nil {
			opts.ContextCompaction = make(map[agent.Ident]ContextCompaction)
		}
		opts.ContextCompaction[id] = *cloneCompaction(&settings)
	}
}

func cloneCompactionOptions(options map[agent.Ident]ContextCompaction) map[agent.Ident]ContextCompaction {
	if options == nil {
		return nil
	}
	owned := make(map[agent.Ident]ContextCompaction, len(options))
	for id, config := range options {
		var err error
		config, err = normalizeCompaction(id, config)
		if err != nil {
			panic(err.Error())
		}
		owned[id] = config
	}
	return owned
}

// normalizeCompaction validates and owns settings from either configuration source.
func normalizeCompaction(id agent.Ident, config ContextCompaction) (ContextCompaction, error) {
	trigger := config.TriggerAt
	if id == "" || trigger.Tokens < 0 || trigger.Bytes < 0 || (trigger.Tokens == 0) != (trigger.Bytes == 0) || trigger.Tokens > trigger.Bytes || config.KeepRecentTokens < 0 || config.ProtectRecentToolTokens < 0 || config.MinSavingTokens < 0 || config.RecallDiscoveryCalls < 0 || config.RecallReadCalls < 0 {
		return ContextCompaction{}, fmt.Errorf("runtime: invalid context compaction settings for %q", id)
	}
	if trigger.Tokens == 0 {
		config.TriggerAt.Tokens, config.TriggerAt.Bytes = 85, 100
	}
	if config.RecallDiscoveryCalls == 0 {
		config.RecallDiscoveryCalls = 20
	}
	if config.RecallReadCalls == 0 {
		config.RecallReadCalls = 20
	}
	return *cloneCompaction(&config), nil
}

func cloneCompaction(settings *ContextCompaction) *ContextCompaction {
	if settings == nil {
		return nil
	}
	owned := *settings
	if settings.ContextModel != nil {
		model := *settings.ContextModel
		owned.ContextModel = &model
	}
	owned.KeepResultTools = slices.Clone(settings.KeepResultTools)
	return &owned
}

func pureCompactionSettings(settings *ContextCompaction) compaction.Settings {
	if settings == nil {
		return compaction.Settings{TriggerAt: contextmeasure.Rate{Tokens: 85, Bytes: 100}, RecallDiscoveryCalls: 20, RecallReadCalls: 20}
	}
	return compaction.Settings{TriggerAt: settings.TriggerAt, KeepRecentTokens: settings.KeepRecentTokens, ProtectRecentToolTokens: settings.ProtectRecentToolTokens, MinSavingTokens: settings.MinSavingTokens, RecallDiscoveryCalls: settings.RecallDiscoveryCalls, RecallReadCalls: settings.RecallReadCalls}
}

func (r *Runtime) stampContextCompaction(input *RunInput) {
	if input == nil {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	input.ContextRecovery = r.agentContextRecovery[input.AgentID]
	input.RecoveryAgents = cloneRecoveryOptions(r.agentContextRecovery)
	input.ContextCompaction = nil
	input.CompactionAgents = cloneCompactionOptions(r.agentContextCompaction)
	if settings, enabled := input.CompactionAgents[input.AgentID]; enabled {
		input.ContextCompaction = cloneCompaction(&settings)
	}
	input.ContextSettings = resolvedContextSettings(input.ContextCompaction, input.ContextRecovery)
}

func inheritCompaction(ctx context.Context, input *RunInput) {
	recovery, _ := ctx.Value(recoverySettingsKey{}).(map[agent.Ident]ContextRecovery)
	input.ContextRecovery = recovery[input.AgentID]
	input.RecoveryAgents = cloneRecoveryOptions(recovery)
	settings, _ := ctx.Value(compactionSettingsKey{}).(map[agent.Ident]ContextCompaction)
	input.CompactionAgents = cloneCompactionOptions(settings)
	if config, enabled := settings[input.AgentID]; enabled {
		input.ContextCompaction = cloneCompaction(&config)
	}
	input.ContextSettings = resolvedContextSettings(input.ContextCompaction, input.ContextRecovery)
}

func resolvedContextSettings(threshold *ContextCompaction, recovery ContextRecovery) compaction.ContextSettings {
	settings := pureCompactionSettings(threshold)
	if settings.TriggerAt.Bytes == 0 {
		settings.TriggerAt = contextmeasure.Rate{Tokens: 85, Bytes: 100}
	}
	if settings.RecallDiscoveryCalls == 0 {
		settings.RecallDiscoveryCalls = 20
	}
	if settings.RecallReadCalls == 0 {
		settings.RecallReadCalls = 20
	}
	margin := recovery.GuardMargin
	if margin.Bytes == 0 {
		margin = contextmeasure.Rate{Tokens: 1, Bytes: 10}
	}
	resolved := compaction.ContextSettings{Settings: settings, Threshold: threshold != nil, GuardMargin: margin, DisableRecall: recovery.DisableRecall}
	if threshold != nil {
		resolved.Summary.Settings = threshold.Summary
		if threshold.ContextModel != nil {
			resolved.Summary.Enabled = true
			resolved.Summary.Model = *threshold.ContextModel
		}
	}
	return resolved
}

func keepCompactionResult(settings *ContextCompaction, tool tools.Ident) bool {
	return settings != nil && slices.Contains(settings.KeepResultTools, tool)
}

func (c compactionWorkflowContext) Context() context.Context {
	ctx := context.WithValue(c.WorkflowContext.Context(), compactionSettingsKey{}, c.settings)
	return context.WithValue(ctx, recoverySettingsKey{}, c.recovery)
}

func (c compactionWorkflowContext) WithCancel() (engine.WorkflowContext, func()) {
	derived, cancel := c.WorkflowContext.WithCancel()
	return compactionWorkflowContext{WorkflowContext: derived, settings: c.settings, recovery: c.recovery}, cancel
}
