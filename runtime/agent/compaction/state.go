// Package compaction makes replayable context decisions from recorded facts and
// immutable index metadata. It performs no content reads or workflow effects.
package compaction

import (
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Settings holds per-run trigger settings. Zero fields resolve to the design
	// defaults against the smallest recorded usable model budget.
	Settings struct {
		TriggerAt               contextmeasure.Rate
		KeepRecentTokens        int
		ProtectRecentToolTokens int
		MinSavingTokens         int
	}
	// ModelState holds the last view-bearing measurements for one model. Rate is
	// replaced only by usage-bearing facts, independently of the latest overhead.
	ModelState struct {
		Rate       contextmeasure.Rate
		Calibrated bool
		Budget     int
		Known      bool
		FixedBytes int
	}
	// Flags holds sticky observations for later workflow event emission.
	Flags struct{ Unmeasured, LimitsUnknown, BudgetTooSmall bool }
	// State is reconstructed from adopted facts and preceding decisions. OpenFrom
	// is the first entry of an open await/confirmation/clarification, set by the
	// caller from workflow state before Decide; zero means no open suffix.
	State struct {
		Models       map[string]ModelState
		MinB         int
		NoProgressAt int
		OpenFrom     index.MessageID
		Flags        Flags
		// Rendered records the last adopted checkpoint measurement.
		Rendered RenderedCheckpoint
	}
	// RenderedCheckpoint qualifies an activity measurement by the rendering inputs.
	RenderedCheckpoint struct {
		Hash     string
		TailFrom index.MessageID
		Budget   index.RenderBudget
		Bytes    int
	}
	// Tier names the strongest transformation requested by a decision.
	Tier string
	// SkipReason explains an attempted or suppressed threshold compaction.
	SkipReason string
	// Decision returns the next view and state without mutating its inputs. A new
	// TailFrom is an eviction intent; Phase 2 stores its receipt before adoption.
	Decision struct {
		View          index.ViewRef
		State         State
		Tier          Tier
		Before, After int
		Skipped       SkipReason
	}
)

const (
	// None leaves both view watermarks unchanged.
	None Tier = "none"
	// Clear advances only the cleared-result watermark.
	Clear Tier = "clear"
	// Evict advances the tail, possibly after clearing results.
	Evict Tier = "evict"
	// NoProgress suppresses repeated attempts until enough estimated growth.
	NoProgress SkipReason = "no_progress"
	// BelowMinSaving reports a tier-one saving too small to advance its watermark.
	BelowMinSaving SkipReason = "below_min_saving"
)

// Observe adopts facts in recorded call order. Non-view-bearing calls never
// change model rates or budgets. MinB only falls, and small-budget disablement
// lasts for the run, even if a later call reports a larger window.
func Observe(state State, facts []contextmeasure.RequestFacts) State {
	state.Models = maps.Clone(state.Models)
	if state.Models == nil {
		state.Models = make(map[string]ModelState)
	}
	bearing := false
	for _, fact := range facts {
		if !fact.ViewBearing {
			continue
		}
		bearing = true
		previous := state.Models[fact.Model]
		if fact.InputTokens > 0 {
			previous.Rate, previous.Calibrated = contextmeasure.Calibrate(fact)
		} else if previous.Rate.Bytes == 0 {
			previous.Rate = contextmeasure.Rate{Tokens: 1, Bytes: 3}
		}
		previous.FixedBytes = max(fact.FixedBytes, 0)
		previous.Known = fact.ContextWindow > 0 && fact.OutputReserve < fact.ContextWindow
		previous.Budget = 0
		state.Flags.BudgetTooSmall = state.Flags.BudgetTooSmall || (fact.ContextWindow > 0 && fact.OutputReserve >= fact.ContextWindow)
		if previous.Known {
			previous.Budget = fact.ContextWindow - max(fact.OutputReserve, 0)
			if state.MinB == 0 || previous.Budget < state.MinB {
				state.MinB = previous.Budget
			}
			state.Flags.BudgetTooSmall = state.Flags.BudgetTooSmall || previous.Budget < 2900
		}
		state.Flags.LimitsUnknown = state.Flags.LimitsUnknown || fact.ContextWindow == 0
		state.Models[fact.Model] = previous
	}
	state.Flags.Unmeasured = state.Flags.Unmeasured || (len(facts) > 0 && !bearing)
	return state
}

func fraction(budget, percent int) int {
	return budget/100*percent + budget%100*percent/100
}

func (s Settings) resolved(budget int) Settings {
	if s.TriggerAt.Tokens <= 0 || s.TriggerAt.Bytes <= 0 {
		s.TriggerAt = contextmeasure.Rate{Tokens: 85, Bytes: 100}
	}
	if s.KeepRecentTokens == 0 {
		s.KeepRecentTokens = min(budget/10, 20000)
	}
	if s.ProtectRecentToolTokens == 0 {
		s.ProtectRecentToolTokens = min(fraction(budget, 15), 40000)
	}
	if s.MinSavingTokens == 0 {
		s.MinSavingTokens = min(budget/10, 20000)
	}
	return s
}
