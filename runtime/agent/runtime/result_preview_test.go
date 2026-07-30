package runtime

import (
	"testing"
	"text/template"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	rthints "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestFormatResultPreviewUsesExplicitResultAndBoundsShape(t *testing.T) {
	toolName := tools.Ident("svc.tools.preview_nested")
	rthints.RegisterResultHint(toolName, template.Must(template.New("preview_nested").Parse(
		`{{ index .Result.Results 0 }} / {{ .Bounds.Returned }} / {{ .Bounds.Total }}`,
	)))

	total := 9
	preview := formatResultPreview(toolName, &projectedRuntimeResult{
		Results: []string{"alpha"},
	}, &agent.Bounds{
		Returned:  1,
		Total:     &total,
		Truncated: true,
	})

	require.Equal(t, "alpha / 1 / 9", preview)
}

func TestFormatResultPreviewLeavesBoundsNilWhenAbsent(t *testing.T) {
	toolName := tools.Ident("svc.tools.preview_nil_bounds")
	rthints.RegisterResultHint(toolName, template.Must(template.New("preview_nil_bounds").Parse(
		`{{ if .Bounds }}has-bounds{{ else }}{{ len .Result.Results }} result{{ end }}`,
	)))

	preview := formatResultPreview(toolName, &projectedRuntimeResult{
		Results: []string{"alpha"},
	}, nil)

	require.Equal(t, "1 result", preview)
}

func TestFormatResultPreviewExposesTypedArgs(t *testing.T) {
	toolName := tools.Ident("svc.tools.preview_args")
	rthints.RegisterResultHint(toolName, template.Must(template.New("preview_args").Parse(
		`{{ .Args.query }} -> {{ .Result.status }}`,
	)))
	rt := &Runtime{
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			toolName: newAnyJSONSpec(toolName, "svc.tools"),
		},
		logger: telemetry.NoopLogger{},
	}

	preview := rt.formatResultPreview(
		t.Context(),
		toolName,
		rawjson.Message([]byte(`{"query":"alpha"}`)),
		map[string]any{"status": "ok"},
		nil,
	)

	require.Equal(t, "alpha -> ok", preview)
}
