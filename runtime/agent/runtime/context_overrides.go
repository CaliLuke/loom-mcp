package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

type (
	// ContextOverrides selects per-run context settings; nil members inherit.
	ContextOverrides = compaction.ContextOverrides
	// Ratio is an exact fraction with a positive denominator.
	Ratio = compaction.Ratio
	// ContextSettingsError names invalid context configuration at start or adoption.
	ContextSettingsError = compaction.ContextSettingsError
	// SummaryPrompt identifies the stored summary template pinned for this run.
	SummaryPrompt = compaction.SummaryPrompt
)

// WithContextOverrides snapshots settings for one run. Children use their own registration.
func WithContextOverrides(overrides ContextOverrides) RunOption {
	owned := overrides.Clone()
	return func(input *RunInput) {
		if input.Policy == nil {
			input.Policy = &PolicyOverrides{}
		}
		copy := owned.Clone()
		input.Policy.Context = &copy
	}
}

// prepareContextStart performs all context I/O before workflow submission.
func (r *Runtime) prepareContextStart(ctx context.Context, input *RunInput) error {
	var overrides *ContextOverrides
	if input.Policy != nil {
		overrides = input.Policy.Context
	}
	if err := compaction.ValidateOverrides(input.ContextSettings, overrides); err != nil {
		return err
	}
	settings := compaction.Merge(input.ContextSettings, overrides)
	if settings.Summary.Enabled {
		if _, ok := r.ModelClient(settings.Summary.Model.ID); !ok {
			return &ContextSettingsError{Field: "ContextModel", Value: settings.Summary.Model.ID, Rule: "must identify a registered model"}
		}
	}
	var err error
	settings.Prompt, err = r.pinContextPrompt(ctx, input, settings)
	if err != nil {
		return err
	}

	hash, err := r.RunStore.Put(ctx, input.RootRunID, compaction.Canonical(settings))
	if err != nil {
		return fmt.Errorf("store context settings: %w", err)
	}
	input.ContextSettings = settings
	input.ContextFingerprint = string(hash)
	// Existing result Keep annotations remain registration-owned; all tunable
	// decisions consume the stamped canonical settings.
	if settings.Threshold && input.ContextCompaction == nil {
		input.ContextCompaction = &api.ContextCompaction{}
	}
	if !settings.Threshold {
		input.ContextCompaction = nil
	}
	input.ContextRecovery.GuardMargin = settings.GuardMargin
	input.ContextRecovery.DisableRecall = settings.DisableRecall
	return nil
}

// recordContextStart returns recorded settings; workflow code never reads prompts.
func (r *Runtime) recordContextStart(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
	start := in.ContextStart
	input := &RunInput{AgentID: start.AgentID, RootRunID: in.RootRunID, SessionID: start.SessionID, Labels: start.Labels, ContextSettings: start.Settings}
	if err := r.prepareContextStart(ctx, input); err != nil {
		return nil, err
	}
	return &api.RecordContentOutput{ContextStart: &api.ContextStartOutput{Settings: input.ContextSettings, Fingerprint: input.ContextFingerprint}}, nil
}

// prepareChildContext records child-scoped prompt resolution on the child's queue.
func prepareChildContext(wf engine.WorkflowContext, route AgentRoute, input *RunInput) error {
	out, err := wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{Name: RecordContentActivityName(route.ID), Options: engine.ActivityOptions{Queue: route.DefaultTaskQueue}, Input: &api.RecordContentInput{RootRunID: input.RootRunID, ContextStart: &api.ContextStartInput{AgentID: input.AgentID, SessionID: input.SessionID, Labels: input.Labels, Settings: input.ContextSettings}}})
	if err != nil {
		return err
	}
	input.ContextSettings = out.ContextStart.Settings
	input.ContextFingerprint = out.ContextStart.Fingerprint
	return nil
}

// pinContextPrompt stores the resolved template once before starting the run.
func (r *Runtime) pinContextPrompt(ctx context.Context, input *RunInput, settings compaction.Settings) (SummaryPrompt, error) {
	if settings.Prompt.Template == "" {
		if err := r.registerSummaryPrompt(input.AgentID, settings.Summary.Settings); err != nil {
			return SummaryPrompt{}, err
		}
		spec, err := r.PromptRegistry.Resolve(ctx, prompt.Ident(string(input.AgentID)+".context.summary"), prompt.Scope{SessionID: input.SessionID, Labels: input.Labels})
		if err != nil {
			return SummaryPrompt{}, err
		}
		hash, err := r.RunStore.Put(ctx, input.RootRunID, []byte(spec.Template))
		if err != nil {
			return SummaryPrompt{}, fmt.Errorf("store context summary prompt: %w", err)
		}
		settings.Prompt = SummaryPrompt{Ref: prompt.PromptRef{ID: spec.ID, Version: spec.Version}, Template: hash}
	} else {
		if _, err := r.RunStore.Get(ctx, input.RootRunID, settings.Prompt.Template); err != nil {
			return SummaryPrompt{}, fmt.Errorf("load overridden summary prompt: %w", err)
		}
	}
	return settings.Prompt, nil
}
