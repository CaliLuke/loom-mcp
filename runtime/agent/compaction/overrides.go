package compaction

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Ratio is an exact rational configuration value. Den must be positive.
	Ratio struct{ Num, Den int }
	// SummaryPrompt pins the resolved summary template in the root content scope.
	SummaryPrompt struct {
		Ref      prompt.PromptRef
		Template runstore.Hash
	}
	// ContextOverrides replaces individual registered settings; nil fields inherit.
	ContextOverrides struct {
		KeepNonTextResident       *bool          `json:"nontext_resident,omitzero"`
		Threshold                 *bool          `json:"threshold,omitzero"`
		MaxTier                   *Tier          `json:"max_tier,omitzero"`
		TriggerAt                 *Ratio         `json:"trigger_at,omitzero"`
		GuardMargin               *Ratio         `json:"guard_margin,omitzero"`
		DisableRecall             *bool          `json:"disable_recall,omitzero"`
		ContextModel              **ContextModel `json:"context_model,omitzero"`
		Summary                   *SummaryPrompt `json:"summary,omitzero"`
		KeepRecentTokens          *int           `json:"keep_recent_tokens,omitzero"`
		KeepUserTokens            *int           `json:"keep_user_tokens,omitzero"`
		ProtectRecentToolTokens   *int           `json:"protect_recent_tool_tokens,omitzero"`
		MinSavingTokens           *int           `json:"min_saving_tokens,omitzero"`
		MaxSummaryTokens          *int           `json:"max_summary_tokens,omitzero"`
		ReceiptTokens             *int           `json:"receipt_tokens,omitzero"`
		FramingTokens             *int           `json:"framing_tokens,omitzero"`
		MaxSummarisedResultTokens *int           `json:"max_summarised_result_tokens,omitzero"`
		MaxPins                   *int           `json:"max_pins,omitzero"`
		MaxPinnedParts            *int           `json:"max_pinned_parts,omitzero"`
		MaxSummaryChunks          *int           `json:"max_summary_chunks,omitzero"`
		RecallDiscoveryCalls      *int           `json:"recall_discovery_calls,omitzero"`
		RecallReadCalls           *int           `json:"recall_read_calls,omitzero"`
		RecallScanBytes           *int           `json:"recall_scan_bytes,omitzero"`
		ContextWindow             *int           `json:"context_window,omitzero"`
	}
	// ContextSettingsError identifies a rejected context setting before further work.
	ContextSettingsError struct {
		Field string
		Value any
		Rule  string
		Model string
		B     int
	}
)

// Summary is the strongest threshold tier; recovery is independent of MaxTier.
const Summary Tier = "summary"

// Merge owns effective settings and retains zero token values as budget rules.
// Call ValidateOverrides before Merge to distinguish explicit zero from inheritance.
func Merge(registered Settings, overrides *ContextOverrides) Settings {
	s := registered
	if overrides == nil {
		return normalizeSettings(s)
	}
	s.KeepRecentTokens = replacement(s.KeepRecentTokens, overrides.KeepRecentTokens)
	s.KeepUserTokens = replacement(s.KeepUserTokens, overrides.KeepUserTokens)
	s.ProtectRecentToolTokens = replacement(s.ProtectRecentToolTokens, overrides.ProtectRecentToolTokens)
	s.MinSavingTokens = replacement(s.MinSavingTokens, overrides.MinSavingTokens)
	s.MaxSummaryTokens = replacement(s.MaxSummaryTokens, overrides.MaxSummaryTokens)
	s.ReceiptTokens = replacement(s.ReceiptTokens, overrides.ReceiptTokens)
	s.FramingTokens = replacement(s.FramingTokens, overrides.FramingTokens)
	s.MaxSummarisedResultTokens = replacement(s.MaxSummarisedResultTokens, overrides.MaxSummarisedResultTokens)
	s.MaxPins = replacement(s.MaxPins, overrides.MaxPins)
	s.MaxPinnedParts = replacement(s.MaxPinnedParts, overrides.MaxPinnedParts)
	s.MaxSummaryChunks = replacement(s.MaxSummaryChunks, overrides.MaxSummaryChunks)
	s.RecallDiscoveryCalls = replacement(s.RecallDiscoveryCalls, overrides.RecallDiscoveryCalls)
	s.RecallReadCalls = replacement(s.RecallReadCalls, overrides.RecallReadCalls)
	s.RecallScanBytes = replacement(s.RecallScanBytes, overrides.RecallScanBytes)
	s.ContextWindow = replacement(s.ContextWindow, overrides.ContextWindow)
	s.Threshold = replacement(s.Threshold, overrides.Threshold)
	if overrides.Threshold != nil {
		s.Default = false
	}
	s.KeepNonTextResident = replacement(s.KeepNonTextResident, overrides.KeepNonTextResident)
	s.MaxTier = replacement(s.MaxTier, overrides.MaxTier)
	s.DisableRecall = replacement(s.DisableRecall, overrides.DisableRecall)

	if overrides.TriggerAt != nil {
		s.TriggerAt = contextmeasure.Rate{Tokens: overrides.TriggerAt.Num, Bytes: overrides.TriggerAt.Den}
	}
	if overrides.GuardMargin != nil {
		s.GuardMargin = contextmeasure.Rate{Tokens: overrides.GuardMargin.Num, Bytes: overrides.GuardMargin.Den}
	}
	if overrides.ContextModel != nil {
		s.Summary.Enabled = *overrides.ContextModel != nil
		s.Summary.Model = ContextModel{}
		if *overrides.ContextModel != nil {
			s.Summary.Model = **overrides.ContextModel
		}
	}
	s.Prompt = replacement(s.Prompt, overrides.Summary)
	return normalizeSettings(s)
}

// ValidateOverrides checks presence-sensitive values and the merged start contract.
func ValidateOverrides(registered Settings, overrides *ContextOverrides) error {
	if overrides != nil {
		if err := validateAbsoluteOverrides(overrides); err != nil {
			return err
		}

		for _, r := range []struct {
			field string
			value *Ratio
		}{{"TriggerAt", overrides.TriggerAt}, {"GuardMargin", overrides.GuardMargin}} {
			if r.value != nil && r.value.Den <= 0 {
				return settingsError(r.field, *r.value, "denominator must be positive")
			}
		}
		if overrides.KeepNonTextResident != nil && !*overrides.KeepNonTextResident && !Merge(registered, overrides).Threshold {
			return settingsError("KeepNonTextResident", false, "requires explicit Threshold")
		}
		if overrides.MaxTier != nil && !Merge(registered, overrides).Threshold {
			return settingsError("MaxTier", *overrides.MaxTier, "requires Threshold")
		}
	}
	s := Merge(registered, overrides)
	if s.TriggerAt.Bytes <= 0 || s.TriggerAt.Tokens <= 0 || s.TriggerAt.Tokens >= s.TriggerAt.Bytes {
		return settingsError("TriggerAt", s.TriggerAt, "must satisfy 0 < ratio < 1")
	}
	if s.GuardMargin.Bytes <= 0 || s.GuardMargin.Tokens < 0 || s.GuardMargin.Tokens > s.GuardMargin.Bytes/2 {
		return settingsError("GuardMargin", s.GuardMargin, "must satisfy 0 <= ratio <= 1/2")
	}
	if s.MaxTier != Clear && s.MaxTier != Evict && s.MaxTier != Summary {
		return settingsError("MaxTier", s.MaxTier, "must be clear, evict or summary")
	}
	if s.MaxSummaryChunks > 64 {
		return settingsError("MaxSummaryChunks", s.MaxSummaryChunks, "must not exceed 64")
	}
	return nil
}

// Error describes both start-time and recorded model-budget failures.
func (e *ContextSettingsError) Error() string {
	return fmt.Sprintf("context setting %s=%v: %s (model=%s B=%d)", e.Field, e.Value, e.Rule, e.Model, e.B)
}

func settingsError(field string, value any, rule string) *ContextSettingsError {
	return &ContextSettingsError{Field: field, Value: value, Rule: rule}
}

func reduce(r contextmeasure.Rate) contextmeasure.Rate {
	if r.Bytes <= 0 || r.Tokens < 0 {
		return r
	}
	a, b := r.Tokens, r.Bytes
	for b != 0 {
		a, b = b, a%b
	}
	return contextmeasure.Rate{Tokens: r.Tokens / a, Bytes: r.Bytes / a}
}

// normalizeSettings resolves only budget-independent defaults.
func normalizeSettings(s Settings) Settings {
	if s.TriggerAt == (contextmeasure.Rate{}) {
		s.TriggerAt = contextmeasure.Rate{Tokens: 17, Bytes: 20}
	}
	if s.GuardMargin == (contextmeasure.Rate{}) {
		s.GuardMargin = contextmeasure.Rate{Tokens: 1, Bytes: 10}
	}
	s.TriggerAt = reduce(s.TriggerAt)
	s.GuardMargin = reduce(s.GuardMargin)
	if s.MaxTier == "" {
		s.MaxTier = Summary
	}
	if s.MaxPins == 0 {
		s.MaxPins = 32
	}
	if s.MaxPinnedParts == 0 {
		s.MaxPinnedParts = 2
	}
	if s.MaxSummaryChunks == 0 {
		s.MaxSummaryChunks = 8
	}
	if s.RecallDiscoveryCalls == 0 {
		s.RecallDiscoveryCalls = 20
	}
	if s.RecallReadCalls == 0 {
		s.RecallReadCalls = 20
	}
	if s.RecallScanBytes == 0 {
		s.RecallScanBytes = 8000000
	}
	if s.FramingTokens == 0 {
		s.FramingTokens = 200
	}
	return s
}

// replacement preserves the registered value when the override is absent.
func replacement[T any](registered T, override *T) T {
	if override != nil {
		return *override
	}
	return registered
}

// validateAbsoluteOverrides rejects explicit zero and negative token and count values.
func validateAbsoluteOverrides(overrides *ContextOverrides) error {
	for _, setting := range []struct {
		field string
		value *int
	}{
		{"KeepRecentTokens", overrides.KeepRecentTokens},
		{"KeepUserTokens", overrides.KeepUserTokens},
		{"ProtectRecentToolTokens", overrides.ProtectRecentToolTokens},
		{"MinSavingTokens", overrides.MinSavingTokens},
		{"MaxSummaryTokens", overrides.MaxSummaryTokens},
		{"ReceiptTokens", overrides.ReceiptTokens},
		{"FramingTokens", overrides.FramingTokens},
		{"MaxSummarisedResultTokens", overrides.MaxSummarisedResultTokens},
		{"MaxPins", overrides.MaxPins},
		{"MaxPinnedParts", overrides.MaxPinnedParts},
		{"MaxSummaryChunks", overrides.MaxSummaryChunks},
		{"RecallDiscoveryCalls", overrides.RecallDiscoveryCalls},
		{"RecallReadCalls", overrides.RecallReadCalls},
		{"RecallScanBytes", overrides.RecallScanBytes},
		{"ContextWindow", overrides.ContextWindow},
	} {
		if setting.value != nil && *setting.value <= 0 {
			return settingsError(setting.field, *setting.value, "must be positive")
		}
	}

	return nil
}
