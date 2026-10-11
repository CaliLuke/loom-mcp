package compaction

import (
	"math"
	"math/bits"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Decide applies tier one before choosing a closed-unit eviction cut. Only
// recorded entry sizes, facts and settings affect its result. Checkpoint size
// is conservatively MaxBytes until the caller can report a rendered size.
func Decide(state State, entries []index.MessageEntry, view index.ViewRef, settings Settings) Decision {
	d := Decision{View: view, State: state, Tier: None}
	if state.MinB == 0 || state.Flags.BudgetTooSmall || state.MinB < 2900 {
		return d
	}
	settings = settings.resolved(state.MinB)
	d.View.Budget = index.RenderBudget{KeepUserTokens: min(state.MinB/20, 10000), MaxSummaryTokens: min(state.MinB/10, 16000), ReceiptTokens: min(fraction(state.MinB, 3), 6000), FramingTokens: 200, PreviewTokens: view.Budget.PreviewTokens}
	d.Before = estimate(state, entries, d.View)
	d.After = d.Before
	if !triggered(state, entries, d.View, settings.TriggerAt) {
		return d
	}
	if state.NoProgressAt > 0 && (d.Before < state.NoProgressAt || d.Before-state.NoProgressAt < settings.MinSavingTokens) {
		d.Skipped = NoProgress
		return d
	}
	applyClear(&d, state, entries, settings)
	applyEviction(&d, state, entries, settings)
	d.After = estimate(state, entries, d.View)
	if triggered(state, entries, d.View, settings.TriggerAt) {
		d.State.NoProgressAt = d.After
		if d.Tier == None && d.Skipped == "" {
			d.Skipped = NoProgress
		}
	} else {
		d.State.NoProgressAt = 0
	}
	return d
}

func applyClear(d *Decision, state State, entries []index.MessageEntry, settings Settings) {
	cleared := clearThrough(state, entries, d.View, settings.ProtectRecentToolTokens)
	if cleared > d.View.ClearedThrough {
		candidate := d.View
		candidate.ClearedThrough = cleared
		saving := d.Before - estimate(state, entries, candidate)
		if saving >= settings.MinSavingTokens {
			d.View = candidate
			d.Tier = Clear
		} else {
			d.Skipped = BelowMinSaving
		}
	}
}

func applyEviction(d *Decision, state State, entries []index.MessageEntry, settings Settings) {
	if triggered(state, entries, d.View, settings.TriggerAt) {
		units := checkpoint.Units(entries, index.RecordedRole, state.OpenFrom)
		tail := checkpoint.ChooseTail(units, func(unit checkpoint.Unit) int {
			return unitTokens(state, entries, d.View, unit)
		}, settings.KeepRecentTokens)
		if tail > max(d.View.TailFrom, 1) && isCut(units, tail) {
			d.View.TailFrom = tail
			d.Tier = Evict
			d.Skipped = ""
		}
	}
}

// clearThrough protects a contiguous newest suffix measured in result tokens.
// Keep results consume suffix space but are never cleared by its watermark.
func clearThrough(state State, entries []index.MessageEntry, view index.ViewRef, protect int) index.MessageID {
	spent := 0
	through := view.ClearedThrough
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.ID < max(view.TailFrom, 1) || entry.ID <= view.ClearedThrough || entry.Kind != index.ToolResult {
			continue
		}
		if spent < protect {
			spent = add(spent, entryTokens(state, entry, view))
			continue
		}
		if !entry.Keep {
			through = max(through, entry.ID)
		}
	}
	return through
}

func estimate(state State, entries []index.MessageEntry, view index.ViewRef) int {
	text, nontext := viewSizes(state, entries, view)
	largest := 0
	for _, model := range state.Models {
		if model.Known {
			largest = max(largest, contextmeasure.Estimate(model.Rate, model.FixedBytes, text, nontext))
		}
	}
	return largest
}

func triggered(state State, entries []index.MessageEntry, view index.ViewRef, trigger contextmeasure.Rate) bool {
	text, nontext := viewSizes(state, entries, view)
	for _, model := range state.Models {
		if model.Known && above(contextmeasure.Estimate(model.Rate, model.FixedBytes, text, nontext), model.Budget, trigger) {
			return true
		}
	}
	return false
}

func viewSizes(state State, entries []index.MessageEntry, view index.ViewRef) (int, int) {
	text, nontext := 0, 0
	for _, entry := range entries {
		if entry.ID >= max(view.TailFrom, 1) || entry.Role == "system" {
			size, parts := entrySize(entry, view)
			text, nontext = add(text, size), add(nontext, parts)
		}
	}
	if view.Checkpoint != nil || view.TailFrom > 1 {
		size := checkpoint.MaxBytes(view.Budget)
		if view.Checkpoint != nil && state.Rendered.Hash == string(*view.Checkpoint) && state.Rendered.TailFrom == view.TailFrom && state.Rendered.Budget == view.Budget {
			size = state.Rendered.Bytes
		}
		text = add(text, size)
	}
	return text, nontext
}

func entrySize(entry index.MessageEntry, view index.ViewRef) (int, int) {
	if entry.Kind == index.ToolResult && entry.ID <= view.ClearedThrough && !entry.Keep {
		id := ""
		if len(entry.ToolUseIDs) > 0 {
			id = entry.ToolUseIDs[0]
		}
		errorText := ""
		if entry.Result != nil && entry.Result.Error != nil {
			errorText = entry.Result.Error.Text
		}
		return len(checkpoint.ClearedResult(entry.ContentBytes(), id, errorText)), 0
	}
	return entry.Bytes, entry.NonText
}

func entryTokens(state State, entry index.MessageEntry, view index.ViewRef) int {
	text, nontext := entrySize(entry, view)
	cost := 0
	for _, model := range state.Models {
		if model.Known {
			cost = max(cost, contextmeasure.Estimate(model.Rate, 0, text, nontext))
		}
	}
	return cost
}

func unitTokens(state State, entries []index.MessageEntry, view index.ViewRef, unit checkpoint.Unit) int {
	text, nontext := 0, 0
	for _, entry := range entries {
		if entry.ID >= unit.From && entry.ID <= unit.Through {
			size, parts := entrySize(entry, view)
			text, nontext = add(text, size), add(nontext, parts)
		}
	}
	cost := 0
	for _, model := range state.Models {
		if model.Known {
			cost = max(cost, contextmeasure.Estimate(model.Rate, 0, text, nontext))
		}
	}
	return cost
}

func isCut(units []checkpoint.Unit, id index.MessageID) bool {
	for _, cut := range checkpoint.CutPoints(units) {
		if cut == id {
			return true
		}
	}
	return false
}

// above compares two full-width integer products without rounding the threshold.
func above(tokens, budget int, rate contextmeasure.Rate) bool {
	lh, ll := bits.Mul64(uint64(max(tokens, 0)), uint64(max(rate.Bytes, 0)))
	rh, rl := bits.Mul64(uint64(max(budget, 0)), uint64(max(rate.Tokens, 0)))
	return lh > rh || (lh == rh && ll > rl)
}
func add(a, b int) int {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}
