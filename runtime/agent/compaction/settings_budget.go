package compaction

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// RenderBudget resolves budget rules without changing the canonical settings.
func RenderBudget(settings Settings, b, preview int) index.RenderBudget {
	s := Merge(settings, nil)
	pins, parts := s.MaxPins, s.MaxPinnedParts
	if pins == 32 {
		pins = 0
	}
	if parts == 2 {
		parts = 0
	}
	return index.RenderBudget{MaxPins: pins, MaxPinnedParts: parts, KeepUserTokens: absoluteOr(s.KeepUserTokens, min(b/20, 10000)), MaxSummaryTokens: absoluteOr(s.MaxSummaryTokens, min(b/10, 16000)), ReceiptTokens: absoluteOr(s.ReceiptTokens, min(fraction(b, 3), 6000)), FramingTokens: s.FramingTokens, PreviewTokens: preview}
}

// ValidateAt checks resident and checkpoint capacity at the smallest adopted B.
func ValidateAt(settings Settings, b int) error {
	s := settings.resolved(b)
	budget := RenderBudget(s, b, 0)
	resident := add(add(add(s.KeepRecentTokens, budget.KeepUserTokens), s.ProtectRecentToolTokens), add(add(budget.MaxSummaryTokens, budget.ReceiptTokens), budget.FramingTokens))
	if resident > b/2 {
		e := settingsError("ResidentTokens", resident, "resident budgets plus framing must not exceed B/2")
		e.B = b
		return e
	}
	if checkpoint.MaxBytes(budget) < checkpoint.MinCheckpointBytes(budget) {
		e := settingsError("CheckpointBytes", checkpoint.MaxBytes(budget), "must fit MinCheckpointBytes")
		e.B = b
		return e
	}
	return nil
}

// EffectiveBudget uses the pretend window only for threshold decisions. Facts
// retain real provider limits for the advisory guard and overflow recovery.
func EffectiveBudget(settings Settings, fact contextmeasure.RequestFacts) (int, error) {
	real := max(0, fact.ContextWindow-fact.OutputReserve)
	if settings.ContextWindow == 0 {
		return real, nil
	}
	pretend := settings.ContextWindow
	b := pretend - max(0, fact.OutputReserve)
	rule := ""
	switch {
	case fact.ContextWindow > 0 && pretend > fact.ContextWindow:
		rule = "must not exceed the real context window"
	case fact.OutputReserve >= pretend:
		rule = "must exceed the output reserve"
	case b < 2900:
		rule = "usable pretend budget must be at least 2900"
	}
	if rule != "" {
		e := settingsError("ContextWindow", pretend, rule)
		e.Model = fact.Model
		e.B = b
		return 0, e
	}
	if fact.ContextWindow > 0 {
		b = min(b, real)
	}
	return b, nil
}

func absoluteOr(value, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}
