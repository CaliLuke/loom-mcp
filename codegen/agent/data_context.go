package codegen

import agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"

type (
	// ContextData contains evaluated context policy for generated registration.
	ContextData struct {
		TriggerNum, TriggerDen, KeepRecentTokens int
		Threshold                                bool
		DisableRecall                            bool
		Summary                                  *SummaryData
		HasSummarize                             bool
	}
	// SummaryData contains literal context summary prompt configuration.
	SummaryData struct {
		Instructions, Prompt string
	}
)

func newContextData(c *agentsExpr.ContextExpr) *ContextData {
	if c == nil {
		return nil
	}
	data := &ContextData{Threshold: !c.Disabled, KeepRecentTokens: c.KeepRecentTokens, DisableRecall: c.RecallSet && !c.Recall}
	if c.TriggerAt != nil {
		data.TriggerNum, data.TriggerDen = c.TriggerAt.Num, c.TriggerAt.Den
	}
	if c.Summarize != nil {
		data.HasSummarize = true
		data.Summary = &SummaryData{Instructions: c.Summarize.Instructions, Prompt: c.Summarize.Prompt}
	}
	return data
}
