package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
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
	}
)

// WithContextCompaction enables threshold compaction. HistoryPolicy cannot be used with it.
func WithContextCompaction(id agent.Ident, settings ContextCompaction) RuntimeOption {
	return func(opts *Options) {
		if opts.ContextCompaction == nil {
			opts.ContextCompaction = make(map[agent.Ident]ContextCompaction)
		}
		opts.ContextCompaction[id] = settings
	}
}

func cloneCompactionOptions(options map[agent.Ident]ContextCompaction) map[agent.Ident]ContextCompaction {
	if options == nil {
		return nil
	}
	owned := make(map[agent.Ident]ContextCompaction, len(options))
	for id, config := range options {
		trigger := config.TriggerAt
		if id == "" || trigger.Tokens < 0 || trigger.Bytes < 0 || (trigger.Tokens == 0) != (trigger.Bytes == 0) || trigger.Tokens > trigger.Bytes || config.KeepRecentTokens < 0 || config.ProtectRecentToolTokens < 0 || config.MinSavingTokens < 0 {
			panic(fmt.Sprintf("runtime: invalid context compaction settings for %q", id))
		}
		if trigger.Tokens == 0 {
			config.TriggerAt.Tokens, config.TriggerAt.Bytes = 85, 100
		}
		config.KeepResultTools = slices.Clone(config.KeepResultTools)
		owned[id] = config
	}
	return owned
}

func cloneCompaction(settings *ContextCompaction) *ContextCompaction {
	if settings == nil {
		return nil
	}
	owned := *settings
	owned.KeepResultTools = slices.Clone(settings.KeepResultTools)
	return &owned
}

func pureCompactionSettings(settings *ContextCompaction) compaction.Settings {
	return compaction.Settings{TriggerAt: settings.TriggerAt, KeepRecentTokens: settings.KeepRecentTokens, ProtectRecentToolTokens: settings.ProtectRecentToolTokens, MinSavingTokens: settings.MinSavingTokens}
}

func (r *Runtime) stampContextCompaction(input *RunInput) {
	if input == nil {
		return
	}
	input.ContextCompaction = nil
	input.CompactionAgents = cloneCompactionOptions(r.contextCompaction)
	if settings, enabled := input.CompactionAgents[input.AgentID]; enabled {
		input.ContextCompaction = cloneCompaction(&settings)
	}
}

func inheritCompaction(ctx context.Context, input *RunInput) {
	settings, _ := ctx.Value(compactionSettingsKey{}).(map[agent.Ident]ContextCompaction)
	input.CompactionAgents = cloneCompactionOptions(settings)
	if config, enabled := settings[input.AgentID]; enabled {
		input.ContextCompaction = cloneCompaction(&config)
	}
}

func keepCompactionResult(settings *ContextCompaction, tool tools.Ident) bool {
	return settings != nil && slices.Contains(settings.KeepResultTools, tool)
}

func (c compactionWorkflowContext) Context() context.Context {
	return context.WithValue(c.WorkflowContext.Context(), compactionSettingsKey{}, c.settings)
}

func (c compactionWorkflowContext) WithCancel() (engine.WorkflowContext, func()) {
	derived, cancel := c.WorkflowContext.WithCancel()
	return compactionWorkflowContext{WorkflowContext: derived, settings: c.settings}, cancel
}
