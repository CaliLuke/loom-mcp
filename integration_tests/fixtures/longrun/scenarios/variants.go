// Package scenarios drives and scores the long-running release criteria.
package scenarios

import (
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
)

// ScenarioContext changes only explicitly threshold-enabled variants.
type ScenarioContext struct{ PretendWindow, RecallDiscoveryCalls, RecallReadCalls, ProtectRecentToolTokens int }

// Variants returns fresh, recorded policy configurations.
func Variants() []eval.Variant {
	out := make([]eval.Variant, 0, 4)
	out = append(out, eval.Variant{ID: "baseline", Policy: &api.PolicyOverrides{Context: &api.ContextOverrides{Threshold: ptr(false)}}})
	for _, spec := range []struct {
		id   string
		tier compaction.Tier
	}{{"clear_only", compaction.Clear}, {"receipt_only", compaction.Evict}, {"hybrid", compaction.Summary}} {
		c := &api.ContextOverrides{Threshold: ptr(true), MaxTier: ptr(spec.tier), ContextWindow: ptr(24000), KeepRecentTokens: ptr(1000), KeepUserTokens: ptr(600), ProtectRecentToolTokens: ptr(512), MinSavingTokens: ptr(100), MaxSummaryTokens: ptr(800)}
		if spec.id == "clear_only" {
			c.DisableRecall = ptr(true)
		}
		if spec.id == "receipt_only" {
			c.ContextModel = new(*compaction.ContextModel)
		}
		out = append(out, eval.Variant{ID: spec.id, Policy: &api.PolicyOverrides{Context: c}})
	}
	return out
}

// Apply is ordered after the variant defaults and never mutates baseline settings.
func (s ScenarioContext) Apply() agenttest.RunOption {
	return func(in *api.RunInput) {
		if in.Policy == nil || in.Policy.Context == nil || in.Policy.Context.Threshold == nil || !*in.Policy.Context.Threshold {
			return
		}
		c := in.Policy.Context.Clone()
		c.ContextWindow = ptr(s.PretendWindow)
		if s.RecallDiscoveryCalls > 0 {
			c.RecallDiscoveryCalls = ptr(s.RecallDiscoveryCalls)
		}
		if s.RecallReadCalls > 0 {
			c.RecallReadCalls = ptr(s.RecallReadCalls)
		}
		if s.ProtectRecentToolTokens > 0 {
			c.ProtectRecentToolTokens = ptr(s.ProtectRecentToolTokens)
		}
		in.Policy.Context = &c
	}
}
func ptr[T any](v T) *T {
	return &v
}
