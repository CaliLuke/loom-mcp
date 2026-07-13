package codegen

import (
	"bytes"
	"path/filepath"
	"text/template"

	"github.com/CaliLuke/loom/codegen"
)

type toolInjectFileData struct {
	Tools []*ToolData
}

func toolsetInjectFile(ts *ToolsetData) *codegen.File {
	if ts == nil || ts.SpecsDir == "" || !toolsetNeedsInject(ts) {
		return nil
	}
	imports := []*codegen.ImportSpec{
		codegen.SimpleImport("fmt"),
		codegen.SimpleImport("github.com/CaliLuke/loom-mcp/runtime/agent/runtime"),
	}
	sections := []codegen.Section{
		codegen.Header(ts.Name+" tool injection helpers", ts.SpecsPackageName, imports),
		toolInjectSection(toolInjectFileData{Tools: ts.Tools}),
	}
	return &codegen.File{Path: filepath.Join(ts.SpecsDir, "inject.go"), Sections: sections}
}

func toolsetNeedsInject(ts *ToolsetData) bool {
	for _, tool := range ts.Tools {
		if tool != nil && len(tool.InjectedFields) > 0 {
			return true
		}
	}
	return false
}

func toolInjectSection(data toolInjectFileData) codegen.Section {
	return codegen.NewRenderSection("tool-inject", func() string {
		funcs := templateFuncMap()
		funcs["isInjectPointer"] = func(tool *ToolData, field string) bool {
			return tool != nil && tool.Args != nil && tool.Args.IsPrimitivePointer(field, true)
		}
		tpl := template.Must(template.New("tool-inject").Funcs(funcs).Parse(toolInjectTemplateSource))
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			panic(err)
		}
		return buf.String()
	})
}

const toolInjectTemplateSource = `
{{- range .Tools }}
{{- if .InjectedFields }}
{{- $tool := . }}
// Inject{{ .ConstName }} populates server-owned fields on {{ .ConstName }}Payload.
func Inject{{ .ConstName }}(payload *{{ .ConstName }}Payload, meta runtime.ToolCallMeta, labels map[string]string) error {
	if payload == nil {
		return fmt.Errorf("{{ .QualifiedName }} payload is nil")
	}
		{{- range $i, $field := .InjectedFields }}
		{{- if isMetaInject $field }}
		{{ goify $field false }}Value := meta.{{ goify $field true }}
		payload.{{ goify $field true }} = {{ if isInjectPointer $tool $field }}&{{ end }}{{ goify $field false }}Value
		{{- else }}
		labelValue{{ $i }}, ok := labels[{{ printf "%q" $field }}]
		if !ok || labelValue{{ $i }} == "" {
			return fmt.Errorf("missing required run label %q for injected field %q", {{ printf "%q" $field }}, {{ printf "%q" $field }})
		}
		payload.{{ goify $field true }} = {{ if isInjectPointer $tool $field }}&{{ end }}labelValue{{ $i }}
		{{- end }}
		{{- end }}
	return nil
}

// Decode{{ .ConstName }} decodes {{ .ConstName }} payload JSON and applies Inject{{ .ConstName }}.
func Decode{{ .ConstName }}(data []byte, meta runtime.ToolCallMeta, labels map[string]string) (*{{ .ConstName }}Payload, error) {
	payload, err := {{ .ConstName }}PayloadCodec.FromJSON(data)
	if err != nil {
		return nil, err
	}
	if err := Inject{{ .ConstName }}(payload, meta, labels); err != nil {
		return nil, err
	}
	return payload, nil
}

{{- end }}
{{- end }}
`
