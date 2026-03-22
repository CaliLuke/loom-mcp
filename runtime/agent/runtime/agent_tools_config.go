package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"text/template"

	agent "goa.design/goa-ai/runtime/agent"
	"goa.design/goa-ai/runtime/agent/model"
	"goa.design/goa-ai/runtime/agent/planner"
	"goa.design/goa-ai/runtime/agent/prompt"
	"goa.design/goa-ai/runtime/agent/run"
	"goa.design/goa-ai/runtime/agent/tools"
)

type (
	// AgentToolOption configures per-tool content for agent-as-tool registrations.
	AgentToolOption func(*AgentToolConfig)

	// PromptBuilder builds a user message for a tool call from its payload when
	// no explicit text or template is configured.
	PromptBuilder func(id tools.Ident, payload any) string

	// AgentToolValidator validates a nested agent-tool call before the child run starts.
	AgentToolValidator func(ctx context.Context, input *AgentToolValidationInput) *AgentToolValidationError

	// AgentToolContent configures the optional consumer-side rendering of the nested agent's initial user message.
	AgentToolContent struct {
		Templates   map[tools.Ident]*template.Template
		Texts       map[tools.Ident]string
		PromptSpecs map[tools.Ident]prompt.Ident
		Prompt      PromptBuilder
	}

	// AgentToolConfig configures how an agent-tool executes.
	AgentToolConfig struct {
		AgentID             agent.Ident
		Route               AgentRoute
		PlanActivityName    string
		ResumeActivityName  string
		ExecuteToolActivity string
		SystemPrompt        string
		AgentToolContent
		PreChildValidator AgentToolValidator
		Name              string
		Description       string
		TaskQueue         string
		Aliases           map[tools.Ident]tools.Ident
	}

	// AgentToolValidationInput captures the data available to PreChildValidator.
	AgentToolValidationInput struct {
		Call      *planner.ToolRequest
		Payload   any
		Messages  []*model.Message
		ParentRun *run.Context
	}

	// AgentToolValidationError reports a tool-scoped validation failure at the agent-as-tool boundary.
	AgentToolValidationError struct {
		message      string
		issues       []*tools.FieldIssue
		descriptions map[string]string
	}
)

// NewAgentToolValidationError constructs a structured validation error for an agent-as-tool pre-child validator.
func NewAgentToolValidationError(message string, issues []*tools.FieldIssue, descriptions map[string]string) *AgentToolValidationError {
	return &AgentToolValidationError{
		message:      message,
		issues:       cloneFieldIssues(issues),
		descriptions: cloneStringMap(descriptions),
	}
}

// Error implements the error interface.
func (e *AgentToolValidationError) Error() string {
	return e.message
}

// Issues returns the structured field issues associated with this validation failure.
func (e *AgentToolValidationError) Issues() []*tools.FieldIssue {
	return cloneFieldIssues(e.issues)
}

// Descriptions returns optional human-readable descriptions for the invalid fields.
func (e *AgentToolValidationError) Descriptions() map[string]string {
	return cloneStringMap(e.descriptions)
}

func cloneFieldIssues(in []*tools.FieldIssue) []*tools.FieldIssue {
	if len(in) == 0 {
		return nil
	}
	out := make([]*tools.FieldIssue, 0, len(in))
	for _, issue := range in {
		if issue == nil {
			continue
		}
		clone := &tools.FieldIssue{
			Field:      issue.Field,
			Constraint: issue.Constraint,
		}
		if len(issue.Allowed) > 0 {
			clone.Allowed = append([]string(nil), issue.Allowed...)
		}
		out = append(out, clone)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// WithText sets plain text content for the given tool ID.
func WithText(id tools.Ident, s string) AgentToolOption {
	return func(c *AgentToolConfig) {
		if c.Texts == nil {
			c.Texts = make(map[tools.Ident]string)
		}
		c.Texts[id] = s
	}
}

// WithTemplate sets a compiled template for the given tool ID.
func WithTemplate(id tools.Ident, t *template.Template) AgentToolOption {
	return func(c *AgentToolConfig) {
		if c.Templates == nil {
			c.Templates = make(map[tools.Ident]*template.Template)
		}
		c.Templates[id] = t
	}
}

// WithPromptSpec configures a prompt registry ID for the given tool ID.
func WithPromptSpec(id tools.Ident, promptID prompt.Ident) AgentToolOption {
	return func(c *AgentToolConfig) {
		if c.PromptSpecs == nil {
			c.PromptSpecs = make(map[tools.Ident]prompt.Ident)
		}
		c.PromptSpecs[id] = promptID
	}
}

// WithTextAll applies the same text to all provided tool IDs.
func WithTextAll(ids []tools.Ident, s string) AgentToolOption {
	return func(c *AgentToolConfig) {
		if c.Texts == nil {
			c.Texts = make(map[tools.Ident]string)
		}
		for _, id := range ids {
			c.Texts[id] = s
		}
	}
}

// WithTemplateAll applies the same template to all provided tool IDs.
func WithTemplateAll(ids []tools.Ident, t *template.Template) AgentToolOption {
	return func(c *AgentToolConfig) {
		if c.Templates == nil {
			c.Templates = make(map[tools.Ident]*template.Template)
		}
		for _, id := range ids {
			c.Templates[id] = t
		}
	}
}

// NewAgentToolsetRegistration creates a toolset registration for an agent-as-tool.
func NewAgentToolsetRegistration(rt *Runtime, cfg AgentToolConfig) ToolsetRegistration {
	return ToolsetRegistration{
		Name:        cfg.Name,
		Description: cfg.Description,
		TaskQueue:   cfg.TaskQueue,
		Inline:      true,
		Execute:     defaultAgentToolExecute(rt, cfg),
		AgentTool:   &cfg,
	}
}

// CompileAgentToolTemplates compiles per-tool message templates from plain strings.
func CompileAgentToolTemplates(raw map[tools.Ident]string, userFuncs template.FuncMap) (map[tools.Ident]*template.Template, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no templates provided")
	}
	funcs := template.FuncMap{
		"tojson": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"join": strings.Join,
	}
	maps.Copy(funcs, userFuncs)
	compiled := make(map[tools.Ident]*template.Template, len(raw))
	for id, src := range raw {
		name := string(id)
		tmpl, err := template.New(name).Funcs(funcs).Option("missingkey=error").Parse(src)
		if err != nil {
			return nil, fmt.Errorf("compile template for %s: %w", id, err)
		}
		compiled[id] = tmpl
	}
	return compiled, nil
}

// ValidateAgentToolTemplates ensures that templates exist for all provided tool IDs.
func ValidateAgentToolTemplates(templates map[tools.Ident]*template.Template, toolIDs []tools.Ident, zeroByTool map[tools.Ident]any) error {
	for _, id := range toolIDs {
		tmpl := templates[id]
		if tmpl == nil {
			return fmt.Errorf("missing template for tool %s", id)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, zeroByTool[id]); err != nil {
			return fmt.Errorf("template validation failed for %s: %w", id, err)
		}
	}
	return nil
}

// ValidateAgentToolCoverage verifies that every tool in toolIDs has exactly one configured content source.
func ValidateAgentToolCoverage(texts map[tools.Ident]string, templates map[tools.Ident]*template.Template, toolIDs []tools.Ident) error {
	for _, id := range toolIDs {
		_, hasText := texts[id]
		_, hasTpl := templates[id]
		if hasText && hasTpl {
			return fmt.Errorf("tool %s configured as both text and template", id)
		}
		if !hasText && !hasTpl {
			return fmt.Errorf("tool %s missing text/template content", id)
		}
	}
	return nil
}

// PayloadToString converts a tool payload to a string for agent consumption.
func PayloadToString(payload any) (string, error) {
	switch v := payload.(type) {
	case string:
		return v, nil
	case json.RawMessage:
		if len(v) == 0 {
			return "", nil
		}
		return string(v), nil
	case nil:
		return "", nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload as JSON: %w", err)
	}
	return string(b), nil
}
