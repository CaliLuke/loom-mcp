package compaction

import (
	"maps"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
)

// SummaryRate selects the largest recorded text rate without map-order influence.
func SummaryRate(state State) contextmeasure.Rate {
	rate := contextmeasure.Rate{Tokens: 1, Bytes: 3}
	found := false
	for _, name := range slices.Sorted(maps.Keys(state.Models)) {
		model := state.Models[name]
		if !model.Known || model.Rate.Bytes <= 0 {
			continue
		}
		if !found || int64(model.Rate.Tokens)*int64(rate.Bytes) > int64(rate.Tokens)*int64(model.Rate.Bytes) {
			rate = model.Rate
			found = true
		}
	}
	return rate
}
