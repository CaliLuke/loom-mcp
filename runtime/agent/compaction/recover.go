package compaction

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Recovery describes a pure view recomputation for a recorded failed request.
type Recovery struct {
	View          index.ViewRef
	Tier          Tier
	Reached       bool
	Reason        string
	Before, After int
	Shortfall     *BudgetShortfallError
}

// BudgetShortfallError names the recorded model budget that cannot render the
// default checkpoint. Recovery never increases that capacity or changes format.
type BudgetShortfallError struct {
	Budget   int `json:"budget"`
	MaxBytes int `json:"max_bytes"`
	MinBytes int `json:"min_bytes"`
}

type recoveryMeasure struct {
	entries    []index.MessageEntry
	failed     contextmeasure.RequestFacts
	rate       contextmeasure.Rate
	budget     int
	calibrated bool
	target     contextmeasure.Rate
}

const (
	// Preview bounds all newest-unit result previews evenly.
	Preview Tier = "preview"
	// Placeholder replaces newest-unit results, including Keep results.
	Placeholder Tier = "placeholder"
	// MinPreviewBytes is the smallest preview before replacing results.
	MinPreviewBytes = 256
)

// Recover uses only the failed request's recorded budget and the adopted rate.
// It ignores threshold protection and saving gates. Checkpoints are charged at
// MaxBytes even when a preceding activity measured a smaller rendering.
func Recover(state State, entries []index.MessageEntry, view index.ViewRef, failure contextmeasure.ContextOverflow, settings Settings) Recovery {
	fact := failure.Failed
	budget := max(0, fact.ContextWindow-fact.OutputReserve)
	settings = settings.resolved(budget)
	m := recoveryMeasure{entries: entries, failed: fact, rate: state.Models[fact.Model].Rate, budget: budget, calibrated: state.Models[fact.Model].Calibrated, target: settings.TriggerAt}
	if m.rate.Bytes <= 0 {
		m.rate = contextmeasure.Rate{Tokens: 1, Bytes: 3}
	}
	r := Recovery{View: view, Tier: None, Before: contextmeasure.Estimate(m.rate, 0, fact.TextBytes, fact.NonText)}
	nextBudget := RecoveryBudget(fact, view.Budget.PreviewTokens)
	if fact.ContextWindow > 0 && checkpoint.MaxBytes(nextBudget) < checkpoint.MinCheckpointBytes(nextBudget) {
		r.Shortfall = &BudgetShortfallError{Budget: budget, MaxBytes: checkpoint.MaxBytes(nextBudget), MinBytes: checkpoint.MinCheckpointBytes(nextBudget)}
		r.Reason, r.After = "budget_too_small", r.Before
		return r
	}
	r.View.Budget = nextBudget
	enough := func() bool {
		return m.enough(&r)
	}
	if recoverClosed(&r, state, entries, view, enough) {
		return r
	}
	units := checkpoint.Units(entries, index.RecordedRole, state.OpenFrom)
	if len(units) == 0 {
		return r
	}
	recoverNewest(&r, entries, newestRecoveryUnit(units), enough)
	return r
}

// RecoveryBudget computes checkpoint defaults from the failed recorded model.
// Unknown windows use the existing component caps, with no live adapter lookup.
func RecoveryBudget(fact contextmeasure.RequestFacts, previewTokens int) index.RenderBudget {
	b := max(0, fact.ContextWindow-fact.OutputReserve)
	if fact.ContextWindow == 0 {
		b = 200000
	}
	return index.RenderBudget{KeepUserTokens: min(b/20, 10000), MaxSummaryTokens: min(b/10, 16000), ReceiptTokens: min(fraction(b, 3), 6000), FramingTokens: 200, PreviewTokens: previewTokens}
}

// Minimal reports whether the latest atomic unit is the only tail and every
// result in it is forcibly cleared. A result-free newest unit is already minimal.
func Minimal(entries []index.MessageEntry, view index.ViewRef) bool {
	units := checkpoint.Units(entries, index.RecordedRole, 0)
	if len(units) == 0 {
		return true
	}
	unit := newestRecoveryUnit(units)
	if max(view.TailFrom, 1) != unit.From {
		return false
	}
	for _, entry := range entries {
		if entry.ID >= unit.From && entry.Kind == index.ToolResult && entry.ID > view.ForceClearThrough {
			return false
		}
	}
	return true
}

// Error explains the byte shortfall while retaining the recorded token budget.
func (e *BudgetShortfallError) Error() string {
	return fmt.Sprintf("context budget %d tokens: checkpoint capacity %d bytes, minimum %d (%d bytes short)", e.Budget, e.MaxBytes, e.MinBytes, e.MinBytes-e.MaxBytes)
}

// Unwrap preserves the checkpoint renderer's budget category.
func (e *BudgetShortfallError) Unwrap() error {
	return checkpoint.ErrCheckpointBudget
}

// recoverNewest shares one preview limit across every newest-unit result.
func recoverNewest(r *Recovery, entries []index.MessageEntry, newest checkpoint.Unit, enough func() bool) {
	last, largest := index.MessageID(0), 0
	for _, entry := range entries {
		if entry.ID >= newest.From && entry.Kind == index.ToolResult {
			last = entry.ID
			largest = max(largest, entry.Bytes)
		}
	}
	if last == 0 {
		r.Reason = "no_results"
		return
	}
	tokens := r.View.Budget.PreviewTokens
	if tokens == 0 {
		tokens = (largest + 1) / 2
	}
	for tokens > MinPreviewBytes/2 {
		tokens = max(tokens/2, MinPreviewBytes/2)
		r.View.Budget.PreviewTokens = tokens
		r.View.PreviewThrough = max(r.View.PreviewThrough, last)
		r.Tier = Preview
		if enough() {
			r.Reached = true
			return
		}
	}
	r.View.ForceClearThrough = max(r.View.ForceClearThrough, last)
	r.Tier = Placeholder
	r.Reached = enough()
	if !r.Reached {
		r.Reason = "minimal"
	}
}

// newestRecoveryUnit joins a trailing user unit to the latest legal cut, as
// ChooseTail does, preserving provider grammar while recovering that suffix.
func newestRecoveryUnit(units []checkpoint.Unit) checkpoint.Unit {
	last := units[len(units)-1]
	cuts := checkpoint.CutPoints(units)
	from := units[0].From
	if len(cuts) > 0 {
		from = cuts[len(cuts)-1]
	}
	return checkpoint.Unit{From: from, Through: last.Through, StartsAssistant: units[len(units)-1].StartsAssistant}
}

func recoverClosed(r *Recovery, state State, entries []index.MessageEntry, view index.ViewRef, enough func() bool) bool {
	// Tier one clears every eligible result, including results in the newest unit.
	for _, entry := range entries {
		if entry.Kind == index.ToolResult && !entry.Keep && entry.ID >= max(view.TailFrom, 1) {
			r.View.ClearedThrough = max(r.View.ClearedThrough, entry.ID)
		}
	}
	if r.View.ClearedThrough > view.ClearedThrough {
		r.Tier = Clear
	}
	if enough() {
		r.Reached = true
		return true
	}
	units := checkpoint.Units(entries, index.RecordedRole, state.OpenFrom)
	for _, cut := range checkpoint.CutPoints(units) {
		if cut > max(r.View.TailFrom, 1) {
			r.View.TailFrom = cut
			r.Tier = Evict
			if enough() {
				r.Reached = true
				return true
			}
		}
	}
	if len(units) == 0 {
		r.Reason = "no_results"
		return false
	}
	return false
}

// enough conservatively charges every checkpoint at its recomputed byte cap.
func (m recoveryMeasure) enough(r *Recovery) bool {
	text, nontext := viewSizes(State{}, m.entries, r.View)
	fixed := add(max(0, m.failed.TextBytes-m.failed.ViewTextBytes), m.failed.RecoveryToolBytes)
	parts := add(max(0, m.failed.NonText-m.failed.ViewNonText), nontext)
	r.After = contextmeasure.Estimate(m.rate, fixed, text, parts)
	// The retry must also pass the fixed-rate provider progress check, even
	// when new tool definitions outweigh a reduction in view bytes.
	if contextmeasure.Estimate(contextmeasure.Rate{}, fixed, text, parts) >= contextmeasure.Estimate(contextmeasure.Rate{}, 0, m.failed.TextBytes, m.failed.NonText) {
		return false
	}
	if m.budget > 0 && m.calibrated {
		return above(m.budget, r.After, contextmeasure.Rate{Tokens: m.target.Bytes, Bytes: m.target.Tokens})
	}
	return text <= m.failed.ViewTextBytes/2
}
