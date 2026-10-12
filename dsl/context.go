package dsl

import (
	"strconv"
	"strings"

	expragents "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// Context configures opt-in threshold compaction inside RunPolicy.
// It cannot be combined with History or a deterministic Workflow.
func Context(fn func()) {
	policy, ok := eval.Current().(*expragents.RunPolicyExpr)
	if !ok {
		incompatibleDSL("Context")
		return
	}
	if policy.Context != nil {
		eval.ReportError("Context already defined for agent %q", policy.Agent.Name)
		return
	}
	c := &expragents.ContextExpr{Policy: policy}
	policy.Context = c
	if fn != nil {
		eval.Execute(fn, c)
	}
}

// TriggerAt sets the fraction of the context window that triggers compaction.
// It appears inside Context and requires 0 < t < 1 with at most four decimals.
func TriggerAt(t float64) {
	c, ok := eval.Current().(*expragents.ContextExpr)
	if !ok {
		incompatibleDSL("TriggerAt")
		return
	}
	if c.TriggerAt != nil {
		eval.ReportError("TriggerAt already defined")
		return
	}
	decimal := strconv.FormatFloat(t, 'f', -1, 64)
	_, fraction, found := strings.Cut(decimal, ".")
	if !(t > 0 && t < 1) || !found || len(fraction) > 4 {
		eval.ReportError("TriggerAt requires 0 < t < 1 with at most 4 decimal places")
		return
	}
	num, err := strconv.Atoi(fraction)
	if err != nil {
		eval.ReportError("invalid TriggerAt: %s", err)
		return
	}
	den := 1
	for range len(fraction) {
		den *= 10
	}
	c.TriggerAt = &expragents.Rate{Num: num, Den: den}
}

// KeepRecentTokens sets a positive recent transcript token budget inside Context.
func KeepRecentTokens(n int) {
	c, ok := eval.Current().(*expragents.ContextExpr)
	if !ok {
		incompatibleDSL("KeepRecentTokens")
		return
	}
	if c.KeepRecentTokens != 0 {
		eval.ReportError("KeepRecentTokens already defined")
		return
	}
	if n <= 0 {
		eval.ReportError("KeepRecentTokens requires n > 0, got %d", n)
		return
	}
	c.KeepRecentTokens = n
}

// Summarize configures summary instructions or a prompt inside Context.
// Generated agent configuration requires ContextModel when Summarize is declared.
func Summarize(fn func()) {
	c, ok := eval.Current().(*expragents.ContextExpr)
	if !ok {
		incompatibleDSL("Summarize")
		return
	}
	if c.Summarize != nil {
		eval.ReportError("Summarize already defined")
		return
	}
	s := &expragents.SummarizeExpr{Context: c}
	c.Summarize = s
	if fn != nil {
		eval.Execute(fn, s)
	}
}

// Recall controls recall advertising inside Context. Recall(false) disables recall.
func Recall(enabled bool) {
	c, ok := eval.Current().(*expragents.ContextExpr)
	if !ok {
		incompatibleDSL("Recall")
		return
	}
	if c.RecallSet {
		eval.ReportError("Recall already defined")
		return
	}
	c.RecallSet, c.Recall = true, enabled
}

// Disable leaves threshold compaction disabled inside Context.
// It may be combined with Recall(false), but not threshold or summary settings.
func Disable() {
	c, ok := eval.Current().(*expragents.ContextExpr)
	if !ok {
		incompatibleDSL("Disable")
		return
	}
	if c.Disabled {
		eval.ReportError("Disable already defined")
		return
	}
	c.Disabled = true
}

// Instructions sets non-empty summary instructions inside Summarize.
func Instructions(text string) {
	s, ok := eval.Current().(*expragents.SummarizeExpr)
	if !ok {
		incompatibleDSL("Instructions")
		return
	}
	if s.Instructions != "" {
		eval.ReportError("Instructions already defined")
		return
	}
	if strings.TrimSpace(text) == "" {
		eval.ReportError("Instructions requires non-empty text")
		return
	}
	s.Instructions = text
}

// Prompt replaces the summary prompt inside Summarize. The template must use
// .Evicted and, when Instructions is declared, .Instructions. It also receives
// .Previous and .Budget. Design validation executes one probe with nonempty
// strings and Budget 100; it checks only the branches taken by those values.
// Test custom templates with representative runtime data, including an empty
// .Previous for the first summary.
func Prompt(text string) {
	s, ok := eval.Current().(*expragents.SummarizeExpr)
	if !ok {
		incompatibleDSL("Prompt")
		return
	}
	if s.Prompt != "" {
		eval.ReportError("Prompt already defined")
		return
	}
	if strings.TrimSpace(text) == "" {
		eval.ReportError("Prompt requires non-empty text")
		return
	}
	s.Prompt = text
}

// KeepResultInContext preserves a tool's whole result during context clearing.
// It appears inside Tool and cannot be combined with Bookkeeping.
func KeepResultInContext() {
	t, ok := eval.Current().(*expragents.ToolExpr)
	if !ok {
		incompatibleDSL("KeepResultInContext")
		return
	}
	if t.KeepResultInContext {
		eval.ReportError("KeepResultInContext already defined")
		return
	}
	t.KeepResultInContext = true
}
