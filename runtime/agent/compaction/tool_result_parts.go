package compaction

import (
	"maps"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ToolResultParts intersects the recorded support of every observed model.
// Model support is fixed on first observation, so adding models never widens it.
func ToolResultParts(state State) model.ToolResultPartSupport {
	var support model.ToolResultPartSupport
	for i, key := range slices.Sorted(maps.Keys(state.Models)) {
		next := state.Models[key].ToolResultParts
		if i == 0 {
			support = next.Intersect(next)
		} else {
			support = support.Intersect(next)
		}
	}
	return support
}

// RecallPartTokens bounds each recalled attachment by the protected result budget.
func (s Settings) RecallPartTokens(budget int) int {
	return s.resolved(budget).ProtectRecentToolTokens
}
