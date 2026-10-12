package api

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"

type (
	// ContextOverrides replaces context settings on one run without child inheritance.
	ContextOverrides = compaction.ContextOverrides
	// Ratio represents an exact fraction with a positive denominator.
	Ratio = compaction.Ratio
	// SummaryPrompt pins a root-scoped summary template and its prompt identity.
	SummaryPrompt = compaction.SummaryPrompt
)
