package api

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"

// PlanToolFilter carries the per-run filters applied before call canonicalization.
// It contains no execution history or content payloads.
type PlanToolFilter struct {
	RestrictToTool tools.Ident
	AllowedTags    []string
	DeniedTags     []string
}
