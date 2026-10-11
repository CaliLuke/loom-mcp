package runtime

import (
	"bytes"
	_ "embed"
	"text/template"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

type (
	// SummarySettings configures summary instructions and the registered baseline.
	SummarySettings   = api.SummarySettings
	summaryPromptData struct {
		Previous, Evicted, Instructions string
		Budget                          int
	}
)

//go:embed summary_prompt.tmpl
var defaultContextSummaryPrompt string

func (r *Runtime) registerSummaryPrompt(id agent.Ident, settings SummarySettings) error {
	r.mu.Lock()
	if r.PromptRegistry == nil {
		r.PromptRegistry = prompt.NewRegistry(nil)
	}
	registry := r.PromptRegistry
	r.mu.Unlock()
	source := settings.Prompt
	if source == "" {
		source = defaultContextSummaryPrompt
	}
	idPrompt := prompt.Ident(string(id) + ".context.summary")
	for _, spec := range registry.List() {
		if spec.ID == idPrompt && spec.AgentID == string(id) && spec.Role == prompt.PromptRoleSystem && spec.Template == source {
			return nil
		}
	}
	return registry.Register(prompt.PromptSpec{ID: prompt.Ident(string(id) + ".context.summary"), AgentID: string(id), Role: prompt.PromptRoleSystem, Template: source})
}

// summaryTemplate snapshots the resolved source; execution is pure during planning.
func summaryTemplate(spec prompt.PromptSpec) (func(summaryPromptData) (string, error), error) {
	t, err := template.New(spec.ID.String()).Option("missingkey=error").Funcs(spec.Funcs).Parse(spec.Template)
	if err != nil {
		return nil, err
	}
	return func(data summaryPromptData) (string, error) {
		var text bytes.Buffer
		if err := t.Execute(&text, data); err != nil {
			return "", err
		}
		return text.String(), nil
	}, nil
}
