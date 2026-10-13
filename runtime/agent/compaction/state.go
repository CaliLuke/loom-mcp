// Package compaction makes replayable context decisions from recorded facts and
// immutable index metadata. It performs no content reads or workflow effects.
package compaction

import (
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// ContextSettings snapshots both opt-in thresholds and always-on recovery.
	ContextSettings = Settings

	// Settings holds per-run trigger settings. Zero fields resolve to the design
	// defaults against the smallest recorded usable model budget.
	Settings struct {
		Default                   bool                `json:"default,omitzero"`
		KeepNonTextResident       bool                `json:"nontext_resident,omitzero"`
		Threshold                 bool                `json:"threshold,omitzero"`
		MaxTier                   Tier                `json:"max_tier,omitzero"`
		GuardMargin               contextmeasure.Rate `json:"guard_margin,omitzero"`
		DisableRecall             bool                `json:"disable_recall,omitzero"`
		Summary                   SummaryConfig       `json:"summary,omitzero"`
		Prompt                    SummaryPrompt       `json:"prompt,omitzero"`
		ContextWindow             int                 `json:"context_window,omitzero"`
		KeepUserTokens            int                 `json:"keep_user_tokens,omitzero"`
		MaxSummaryTokens          int                 `json:"max_summary_tokens,omitzero"`
		ReceiptTokens             int                 `json:"receipt_tokens,omitzero"`
		FramingTokens             int                 `json:"framing_tokens,omitzero"`
		MaxSummarisedResultTokens int                 `json:"max_summarised_result_tokens,omitzero"`
		MaxPins                   int                 `json:"max_pins,omitzero"`
		MaxPinnedParts            int                 `json:"max_pinned_parts,omitzero"`
		MaxSummaryChunks          int                 `json:"max_summary_chunks,omitzero"`
		RecallScanBytes           int                 `json:"recall_scan_bytes,omitzero"`
		TriggerAt                 contextmeasure.Rate
		KeepRecentTokens          int `json:"keep_recent_tokens,omitzero"`
		ProtectRecentToolTokens   int `json:"protect_recent_tool_tokens,omitzero"`
		MinSavingTokens           int `json:"min_saving_tokens,omitzero"`
		RecallDiscoveryCalls      int
		RecallReadCalls           int
	}
	// ModelState holds the last view-bearing measurements for one model. Rate is
	// replaced only by usage-bearing facts, independently of the latest overhead.
	ModelState struct {
		Qualified       contextmeasure.QualificationTier
		ProviderConfig  string
		FirstCall       runstore.CallKey
		ToolResultParts model.ToolResultPartSupport
		Rate            contextmeasure.Rate
		Calibrated      bool
		Budget          int
		Known           bool
		FixedBytes      int
	}
	// Flags holds sticky observations for later workflow event emission.
	Flags struct{ Unmeasured, LimitsUnknown, BudgetTooSmall, NonTextResident, UnqualifiedStep bool }
	// State is reconstructed from adopted facts and preceding decisions. OpenFrom
	// is the first entry of an open await/confirmation/clarification, set by the
	// caller from workflow state before Decide; zero means no open suffix.
	State struct {
		// Compatibility holds the last durably adopted attachment check.
		Compatibility Compatibility
		// ValidateBudgets enables strict mid-run validation for explicit run overrides.
		// This provenance is separate from the canonical effective settings.
		ValidateBudgets bool
		// InvalidLimitsModel retains the first invalid override diagnostic identity.
		InvalidLimitsModel string
		Summary            SummaryState
		Recall             RecallAllowances
		Models             map[string]ModelState
		MinB               int
		// EffectiveWindow is the window of the fact that established MinB.
		LimitingModel   string
		EffectiveWindow int
		NoProgressAt    int
		OpenFrom        index.MessageID
		Flags           Flags
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
	Tier = contextmeasure.QualificationTier
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
		if fact.LimitsInvalid && state.InvalidLimitsModel == "" {
			state.InvalidLimitsModel = fact.Model
		}
		previous := observedModel(state.Models, fact)
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
			observeBudget(&state, fact, previous.Budget)
		}
		state.Flags.LimitsUnknown = state.Flags.LimitsUnknown || fact.ContextWindow == 0 || fact.LimitsInvalid
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

// observedModel fixes capability support on the model's first view-bearing fact.
func observedModel(models map[string]ModelState, fact contextmeasure.RequestFacts) ModelState {
	if previous, seen := models[fact.Model]; seen {
		return previous
	}
	return ModelState{ToolResultParts: fact.ToolResultParts.Intersect(fact.ToolResultParts), Qualified: fact.Qualified, ProviderConfig: fact.ProviderConfig, FirstCall: fact.Call}
}

func observeBudget(state *State, fact contextmeasure.RequestFacts, budget int) {
	if state.MinB == 0 || budget < state.MinB {
		state.MinB = budget
		state.EffectiveWindow = fact.ContextWindow
		state.LimitingModel = fact.Model
	} else if budget == state.MinB && (state.LimitingModel == "" || fact.Model < state.LimitingModel) {
		state.LimitingModel = fact.Model
	}
	state.Flags.BudgetTooSmall = state.Flags.BudgetTooSmall || budget < 2900
}
