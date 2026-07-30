package dsl

import (
	expragents "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// History defines how the agent runtime manages conversation history before
// each planner invocation. It can either:
//
//   - KeepRecentTurns(N) to retain only the last N turns, or
//   - CompressAt... plus KeepMax... to summarize older turns while preserving
//     an exact recent tail.
//
// CompressAtMaxInputTokens and CompressAtTurns decide when summarization runs,
// while KeepMaxInputTokens and KeepMaxTurns decide which newest complete turns
// stay exact after summarization.
//
// At most one history policy may be configured per agent.
//
// History must appear inside a RunPolicy expression.
//
// Example:
//
//	RunPolicy(func() {
//	    History(func() {
//	        KeepRecentTurns(20)
//	    })
//	})
func History(fn func()) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("History")
		return
	}
	if policy.History != nil {
		eval.ReportError("History already defined for agent %q", policy.Agent.Name)
		return
	}
	h := &expragents.HistoryExpr{
		Policy: policy,
	}
	policy.History = h
	if fn != nil {
		eval.Execute(fn, h)
	}
}

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

// KeepRecentTurns configures a history policy that retains only the most recent
// N user/assistant turns while preserving system prompts and tool exchanges.
//
// KeepRecentTurns must appear inside a History expression.
//
// Example:
//
//	RunPolicy(func() {
//	    History(func() {
//	        KeepRecentTurns(20)
//	    })
//	})
func KeepRecentTurns(n int) {
	h, ok := eval.Current().(*expragents.HistoryExpr)
	if !ok {
		incompatibleDSL("KeepRecentTurns")
		return
	}
	if h.Mode != "" {
		eval.ReportError("only one history policy may be configured per agent")
		return
	}
	if n <= 0 {
		eval.ReportError("KeepRecentTurns requires n > 0, got %d", n)
		return
	}
	h.Mode = expragents.HistoryModeKeepRecent
	h.KeepRecent = n
}

// CompressAtTurns configures compression to run once at least n logical turns
// have accumulated. It is optional when CompressAtMaxInputTokens is set.
//
// CompressAtTurns must appear inside a History expression.
func CompressAtTurns(n int) {
	h := compressHistory("CompressAtTurns")
	if h == nil {
		return
	}
	if n <= 0 {
		eval.ReportError("CompressAtTurns requires n > 0, got %d", n)
		return
	}
	h.CompressAtTurns = n
}

// CompressAtMaxInputTokens configures compression to run when the
// provider-visible transcript exceeds n exact input tokens.
//
// CompressAtMaxInputTokens must appear inside a History expression.
func CompressAtMaxInputTokens(n int) {
	h := compressHistory("CompressAtMaxInputTokens")
	if h == nil {
		return
	}
	if n <= 0 {
		eval.ReportError("CompressAtMaxInputTokens requires n > 0, got %d", n)
		return
	}
	h.CompressAtMaxInputTokens = n
}

// KeepMaxTurns caps the exact retention tail to at most n newest logical turns
// after compression.
//
// KeepMaxTurns must appear inside a History expression with a compression
// trigger.
func KeepMaxTurns(n int) {
	h := compressHistory("KeepMaxTurns")
	if h == nil {
		return
	}
	if n <= 0 {
		eval.ReportError("KeepMaxTurns requires n > 0, got %d", n)
		return
	}
	h.KeepMaxTurns = n
}

// KeepMaxInputTokens keeps the newest whole logical turns whose exact transcript
// cost fits within n input tokens after compression.
//
// KeepMaxInputTokens must appear inside a History expression with a compression
// trigger.
func KeepMaxInputTokens(n int) {
	h := compressHistory("KeepMaxInputTokens")
	if h == nil {
		return
	}
	if n <= 0 {
		eval.ReportError("KeepMaxInputTokens requires n > 0, got %d", n)
		return
	}
	h.KeepMaxInputTokens = n
}

func compressHistory(name string) *expragents.HistoryExpr {
	h, ok := eval.Current().(*expragents.HistoryExpr)
	if !ok {
		incompatibleDSL(name)
		return nil
	}
	switch h.Mode {
	case "":
		h.Mode = expragents.HistoryModeCompress
	case expragents.HistoryModeCompress:
	case expragents.HistoryModeKeepRecent:
		eval.ReportError("%s cannot be combined with KeepRecentTurns", name)
		return nil
	default:
		eval.ReportError("unknown history policy mode %q", h.Mode)
		return nil
	}
	return h
}
