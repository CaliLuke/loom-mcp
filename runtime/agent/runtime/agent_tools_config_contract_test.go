package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestAgentToolValidationErrorDetachesMetadata(t *testing.T) {
	allowed := []string{"fast", "safe"}
	issues := []*tools.FieldIssue{{Field: "mode", Constraint: "enum", Allowed: allowed}, nil}
	descriptions := map[string]string{"mode": "execution mode"}
	err := NewAgentToolValidationError("invalid mode", issues, descriptions)

	allowed[0] = "mutated"
	issues[0].Field = "mutated"
	descriptions["mode"] = "mutated"
	require.EqualError(t, err, "invalid mode")
	assert.Equal(t, "mode", err.Issues()[0].Field)
	assert.Equal(t, []string{"fast", "safe"}, err.Issues()[0].Allowed)
	assert.Equal(t, "execution mode", err.Descriptions()["mode"])

	returnedIssues := err.Issues()
	returnedIssues[0].Allowed[0] = "changed"
	returnedDescriptions := err.Descriptions()
	returnedDescriptions["mode"] = "changed"
	assert.Equal(t, []string{"fast", "safe"}, err.Issues()[0].Allowed)
	assert.Equal(t, "execution mode", err.Descriptions()["mode"])
}

func TestAgentToolContentOptionsApplyPerToolAndAcrossTools(t *testing.T) {
	first := tools.Ident("agents.first")
	second := tools.Ident("agents.second")
	tmpl := template.Must(template.New("message").Parse("hello"))
	cfg := AgentToolConfig{}
	options := []AgentToolOption{
		WithText(first, "first text"),
		WithTemplate(first, tmpl),
		WithPromptSpec(first, "prompts.first"),
		WithTextAll([]tools.Ident{first, second}, "shared text"),
		WithTemplateAll([]tools.Ident{first, second}, tmpl),
	}
	for _, option := range options {
		option(&cfg)
	}

	assert.Equal(t, "shared text", cfg.Texts[first])
	assert.Equal(t, "shared text", cfg.Texts[second])
	assert.Same(t, tmpl, cfg.Templates[first])
	assert.Same(t, tmpl, cfg.Templates[second])
	assert.Equal(t, prompt.Ident("prompts.first"), cfg.PromptSpecs[first])
}

func TestCompileAndValidateAgentToolTemplates(t *testing.T) {
	toolID := tools.Ident("agents.search")
	compiled, err := CompileAgentToolTemplates(map[tools.Ident]string{
		toolID: `{{upper .query}} {{join .tags ","}} {{tojson .filters}}`,
	}, template.FuncMap{"upper": strings.ToUpper})
	require.NoError(t, err)
	require.NoError(t, ValidateAgentToolTemplates(compiled, []tools.Ident{toolID}, map[tools.Ident]any{
		toolID: map[string]any{
			"query":   "status",
			"tags":    []string{"fast", "safe"},
			"filters": map[string]any{"open": true},
		},
	}))
	var rendered strings.Builder
	require.NoError(t, compiled[toolID].Execute(&rendered, map[string]any{
		"query":   "status",
		"tags":    []string{"fast", "safe"},
		"filters": map[string]any{"open": true},
	}))
	assert.Equal(t, `STATUS fast,safe {"open":true}`, rendered.String())

	_, err = CompileAgentToolTemplates(nil, nil)
	require.EqualError(t, err, "no templates provided")
	_, err = CompileAgentToolTemplates(map[tools.Ident]string{toolID: "{{"}, nil)
	require.ErrorContains(t, err, "compile template for agents.search")
	require.ErrorContains(t, ValidateAgentToolTemplates(compiled, []tools.Ident{"agents.missing"}, nil), "missing template")

	missingKey, err := CompileAgentToolTemplates(map[tools.Ident]string{toolID: "{{.missing}}"}, nil)
	require.NoError(t, err)
	require.ErrorContains(t, ValidateAgentToolTemplates(missingKey, []tools.Ident{toolID}, map[tools.Ident]any{toolID: map[string]any{}}), "template validation failed")
}

func TestValidateAgentToolCoverageAndPayloadToString(t *testing.T) {
	toolID := tools.Ident("agents.search")
	tmpl := template.Must(template.New("message").Parse("hello"))
	require.NoError(t, ValidateAgentToolCoverage(map[tools.Ident]string{toolID: "text"}, nil, []tools.Ident{toolID}))
	require.NoError(t, ValidateAgentToolCoverage(nil, map[tools.Ident]*template.Template{toolID: tmpl}, []tools.Ident{toolID}))
	require.ErrorContains(t, ValidateAgentToolCoverage(
		map[tools.Ident]string{toolID: "text"},
		map[tools.Ident]*template.Template{toolID: tmpl},
		[]tools.Ident{toolID},
	), "configured as both")
	require.ErrorContains(t, ValidateAgentToolCoverage(nil, nil, []tools.Ident{toolID}), "missing text/template")

	text, err := PayloadToString("ready")
	require.NoError(t, err)
	assert.Equal(t, "ready", text)
	text, err = PayloadToString(json.RawMessage(`{"ready":true}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"ready":true}`, text)
	text, err = PayloadToString(json.RawMessage(nil))
	require.NoError(t, err)
	assert.Empty(t, text)
	text, err = PayloadToString(nil)
	require.NoError(t, err)
	assert.Empty(t, text)
	text, err = PayloadToString(map[string]any{"ready": true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ready":true}`, text)
	_, err = PayloadToString(make(chan int))
	require.ErrorContains(t, err, "marshal payload as JSON")
}
