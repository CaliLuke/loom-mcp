package run

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"

// ContextRecovery contains runtime-stamped overflow and lookup settings.
// DisableRecall's zero value enables context lookup.
type ContextRecovery struct {
	// GuardMargin is the advisory excess fraction; zero resolves to 1/10.
	GuardMargin   contextmeasure.Rate `json:"guard_margin"`
	DisableRecall bool                `json:"disable_recall"`
}
