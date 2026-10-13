package dsl

import (
	expragents "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// Cache defines the prompt caching policy for the current agent. It configures
// where the runtime should place cache checkpoints relative to system prompts
// and tool definitions for providers that support caching.
//
// Cache must appear inside a RunPolicy expression.
//
// Example:
//
//	RunPolicy(func() {
//	    Cache(func() {
//	        AfterSystem()
//	        AfterTools()
//	    })
//	})
func Cache(fn func()) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("Cache")
		return
	}
	if policy.Cache != nil {
		eval.ReportError("Cache already defined for agent %q", policy.Agent.Name)
		return
	}
	c := &expragents.CacheExpr{
		Policy: policy,
	}
	policy.Cache = c
	if fn != nil {
		eval.Execute(fn, c)
	}
}

// AfterSystem configures the cache policy to place a checkpoint after all
// system messages. Providers that support prompt caching interpret this as a
// cache boundary immediately following the system preamble.
//
// AfterSystem must appear inside a Cache expression.
func AfterSystem() {
	cache, ok := eval.Current().(*expragents.CacheExpr)
	if !ok {
		incompatibleDSL("AfterSystem")
		return
	}
	cache.AfterSystem = true
}

// AfterTools configures the cache policy to place a checkpoint after tool
// definitions. Providers that support tool-level cache checkpoints interpret
// this as a cache boundary immediately following the tool configuration
// section.
//
// AfterTools must appear inside a Cache expression.
func AfterTools() {
	cache, ok := eval.Current().(*expragents.CacheExpr)
	if !ok {
		incompatibleDSL("AfterTools")
		return
	}
	cache.AfterTools = true
}
