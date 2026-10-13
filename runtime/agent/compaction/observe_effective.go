package compaction

import (
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
)

// ObserveEffective adopts real facts using the immutable run's trigger window.
// It checks budgets when a view-bearing model first supplies B or lowers MinB.
// Only explicit run overrides or a pretend window make a shortfall fatal.
func ObserveEffective(state State, facts []contextmeasure.RequestFacts, settings Settings) (State, error) {
	for _, fact := range facts {
		if !fact.ViewBearing {
			state = Observe(state, []contextmeasure.RequestFacts{fact})
			continue
		}
		budget, err := EffectiveBudget(settings, fact)
		if err != nil {
			return state, err
		}
		if settings.Threshold && fact.ContextWindow == 0 && settings.ContextWindow == 0 {
			return Observe(state, []contextmeasure.RequestFacts{fact}), &ContextSettingsError{Field: "ContextWindow", Value: 0, Rule: "threshold compaction requires known limits or a pretend window", Model: fact.Model}
		}
		if (state.ValidateBudgets || settings.ContextWindow > 0) && budget > 0 && (state.MinB == 0 || budget < state.MinB) {
			if err := ValidateAt(settings, budget); err != nil {
				var e *ContextSettingsError
				if !errors.As(err, &e) {
					return state, err
				}
				e.Model = fact.Model
				return state, e
			}
		}
		effective := fact
		if settings.ContextWindow > 0 {
			effective.ContextWindow = budget + fact.OutputReserve
		}
		state = Observe(state, []contextmeasure.RequestFacts{effective})
	}
	if settings.Default && ObservedQualifiedTier(state) == "" {
		state.Flags.UnqualifiedStep = true
	}
	return state, nil
}
