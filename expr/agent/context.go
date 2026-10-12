package agent

import (
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/CaliLuke/loom/eval"
)

type (
	// Rate is an exact decimal fraction authored in the Context DSL.
	Rate struct {
		Num, Den int
	}
	// ContextExpr configures context compaction and recall for one agent.
	ContextExpr struct {
		Policy            *RunPolicyExpr
		TriggerAt         *Rate
		KeepRecentTokens  int
		Summarize         *SummarizeExpr
		RecallSet, Recall bool
		Disabled          bool
	}
	// SummarizeExpr configures the summary prompt for a context policy.
	SummarizeExpr struct {
		Context              *ContextExpr
		Instructions, Prompt string
	}
	// contextPromptProbe records accesses made by an executed summary template.
	contextPromptProbe struct {
		evicted, instructions bool
	}
)

// EvalName describes the context policy for diagnostics.
func (c *ContextExpr) EvalName() string {
	return fmt.Sprintf("context policy for agent %q", c.Policy.Agent.Name)
}

// EvalName describes the summary policy for diagnostics.
func (s *SummarizeExpr) EvalName() string {
	return fmt.Sprintf("context summary for agent %q", s.Context.Policy.Agent.Name)
}

// Previous provides a nonempty prior summary during prompt validation.
func (*contextPromptProbe) Previous() string {
	return "previous"
}

// Evicted records access to the history being summarized.
func (p *contextPromptProbe) Evicted() string {
	p.evicted = true
	return "evicted"
}

// Instructions records access to the application summary instructions.
func (p *contextPromptProbe) Instructions() string {
	p.instructions = true
	return "instructions"
}

// Budget provides the summary token budget during prompt validation.
func (*contextPromptProbe) Budget() int {
	return 100
}

// validateContextTool checks the retention and bookkeeping contract.
func validateContextTool(t *ToolExpr, verr *eval.ValidationErrors) {
	if t.Bookkeeping && t.KeepResultInContext {
		verr.Add(t, "KeepResultInContext cannot be combined with Bookkeeping")
	}
}

// validateContext checks cross-declaration constraints after design evaluation.
func (r *RunPolicyExpr) validateContext(verr *eval.ValidationErrors) {
	c := r.Context
	if c == nil {
		return
	}
	if r.History != nil {
		verr.Add(c, "Context cannot be combined with History")
	}
	if r.Agent.Workflow != nil {
		verr.Add(c, "Context requires a planner agent and cannot be combined with Workflow")
	}
	if c.Disabled && (c.TriggerAt != nil || c.KeepRecentTokens != 0 || c.Summarize != nil) {
		verr.Add(c, "Disable cannot be combined with TriggerAt, KeepRecentTokens or Summarize")
	}
	if r.Agent.Used != nil {
		for _, ts := range r.Agent.Used.Toolsets {
			for _, tool := range ts.Tools {
				if strings.Contains(tool.Name, "recall_context") {
					verr.Add(c, "Context reserves tool names containing recall_context")
				}
			}
		}
	}
	if c.Summarize != nil {
		c.Summarize.validate(verr)
	}
}

// validate checks the summary template against its runtime data contract.
func (s *SummarizeExpr) validate(verr *eval.ValidationErrors) {
	if s.Instructions == "" && s.Prompt == "" {
		verr.Add(s, "Summarize requires Instructions or Prompt")
		return
	}
	if s.Prompt == "" {
		return
	}
	// Summary baselines register no custom functions in the prompt registry.
	t, err := template.New("context.summary").Option("missingkey=error").Parse(s.Prompt)
	if err != nil {
		verr.Add(s, "invalid context summary Prompt: %s", err)
		return
	}
	data := &contextPromptProbe{}
	if err := t.Execute(io.Discard, data); err != nil {
		message := strings.ReplaceAll(err.Error(), fmt.Sprintf("%T", data), "summary prompt data")
		verr.Add(s, "invalid context summary Prompt: %s", message)
		return
	}
	if !data.evicted {
		verr.Add(s, "context summary Prompt must reference .Evicted")
	}
	if s.Instructions != "" && !data.instructions {
		verr.Add(s, "context summary Prompt must reference .Instructions when Instructions is declared")
	}
}
