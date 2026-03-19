{{- if .Tools }}
{{ comment "Tools handling" }}

func decodeMCPPayloadStrict(data []byte, payload any) error {
    dec := json.NewDecoder(bytes.NewReader(data))
    dec.DisallowUnknownFields()
    if err := dec.Decode(payload); err != nil {
        return err
    }
    if err := dec.Decode(&struct{}{}); err != io.EOF {
        if err == nil {
            return fmt.Errorf("unexpected trailing JSON data")
        }
        return err
    }
    return nil
}

func topLevelJSONFieldSet(raw json.RawMessage) (map[string]struct{}, error) {
    fields := make(map[string]struct{})
    if len(bytes.TrimSpace(raw)) == 0 {
        return fields, nil
    }
    var payload map[string]json.RawMessage
    if err := json.Unmarshal(raw, &payload); err != nil {
        return nil, err
    }
    for name := range payload {
        fields[name] = struct{}{}
    }
    return fields, nil
}

func decodeMCPPayloadFields(data []byte) (map[string]json.RawMessage, error) {
    var fields map[string]json.RawMessage
    if err := json.Unmarshal(data, &fields); err != nil {
        return nil, err
    }
    if fields == nil {
        fields = make(map[string]json.RawMessage)
    }
    return fields, nil
}

func validateMCPPayloadRequired(fields map[string]json.RawMessage, field string) error {
    raw, ok := fields[field]
    if !ok {
        return goa.PermanentError("invalid_params", "Missing required field: %s", field)
    }
    trimmed := bytes.TrimSpace(raw)
    if bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("null")) {
        return goa.PermanentError("invalid_params", "Missing required field: %s", field)
    }
    return nil
}

func validateMCPPayloadEnum(fields map[string]json.RawMessage, field string, allowed ...string) error {
    raw, ok := fields[field]
    if !ok {
        return nil
    }
    var value any
    if err := json.Unmarshal(raw, &value); err != nil {
        return err
    }
    actual := fmt.Sprint(value)
    for _, candidate := range allowed {
        if actual == candidate {
            return nil
        }
    }
    return goa.PermanentError("invalid_params", "Invalid value for %s", field)
}

func (a *MCPAdapter) ToolsList(ctx context.Context, p *ToolsListPayload) (res *ToolsListResult, err error) {
    ctx, span, start, attrs := a.startTelemetry(ctx, "tools/list")
    defer func() {
        a.finishTelemetry(ctx, span, start, attrs, err, false)
    }()
    if !a.isInitialized(ctx) {
        return nil, goa.PermanentError("invalid_params", "Not initialized")
    }
    a.log(ctx, "request", map[string]any{"method": "tools/list"})
    tools := []*ToolInfo{
        {{- range .Tools }}
        {
            Name: {{ quote .Name }},
            Description: stringPtr({{ quote .Description }}),
            {{- if .InputSchema }}
            InputSchema: json.RawMessage([]byte({{ printf "%q" .InputSchema }})),
            {{- else }}
            InputSchema: json.RawMessage([]byte("{\"type\":\"object\",\"properties\":{},\"additionalProperties\":false}")),
            {{- end }}
            {{- if .AnnotationsJSON }}
            Annotations: json.RawMessage([]byte({{ printf "%q" .AnnotationsJSON }})),
            {{- end }}
        },
        {{- end }}
    }
    res = &ToolsListResult{Tools: tools}
    a.log(ctx, "response", map[string]any{"method": "tools/list"})
    return res, nil
}

{{ if .ToolsCallStreaming }}
{{- range .Tools }}
{{- if .IsStreaming }}
type {{ goify .OriginalMethodName }}StreamBridge struct {
    out ToolsCallServerStream
    adapter *MCPAdapter
}
func (b *{{ goify .OriginalMethodName }}StreamBridge) Send(ctx context.Context, ev {{ $.Package }}.{{ .StreamEventType }}) error {
    s, e := mcpruntime.EncodeJSONToString(ctx, goahttp.ResponseEncoder, ev)
    if e != nil {
        return e
    }
    return b.out.Send(ctx, &ToolsCallResult{
        Content: []*ContentItem{
            buildContentItem(b.adapter, s),
        },
    })
}
func (b *{{ goify .OriginalMethodName }}StreamBridge) SendAndClose(ctx context.Context, ev {{ $.Package }}.{{ .StreamEventType }}) error {
    s, e := mcpruntime.EncodeJSONToString(ctx, goahttp.ResponseEncoder, ev)
    if e != nil {
        return e
    }
    return b.out.SendAndClose(ctx, &ToolsCallResult{
        Content: []*ContentItem{
            buildContentItem(b.adapter, s),
        },
    })
}
func (b *{{ goify .OriginalMethodName }}StreamBridge) SendError(ctx context.Context, id string, err error) error {
    return b.out.SendError(ctx, id, err)
}
{{- end }}
{{- end }}

func (a *MCPAdapter) ToolsCall(ctx context.Context, p *ToolsCallPayload, stream ToolsCallServerStream) (err error) {
    attrs := []attribute.KeyValue{}
    if p != nil && p.Name != "" {
        attrs = append(attrs,
            attribute.String("mcp.tool", p.Name),
            attribute.String("tool", p.Name),
        )
    }
    ctx, span, start, attrs := a.startTelemetry(ctx, "tools/call", attrs...)
    toolErr := false
    defer func() {
        a.finishTelemetry(ctx, span, start, attrs, err, toolErr)
    }()
    info := a.toolCallInfo(p)
    handler := a.wrapToolCallHandler(info, a.toolsCallHandler)
    toolErr, err = handler(ctx, p, stream)
    return err
}

func (a *MCPAdapter) toolsCallHandler(ctx context.Context, p *ToolsCallPayload, stream ToolsCallServerStream) (toolErr bool, err error) {
    if !a.isInitialized(ctx) {
        return false, goa.PermanentError("invalid_params", "Not initialized")
    }
    name := ""
    if p != nil {
        name = p.Name
    }
    a.log(ctx, "request", map[string]any{"method": "tools/call", "name": name})
    switch p.Name {
    {{- range .Tools }}
    case {{ quote .Name }}:
        {{- if .HasPayload }}
        {{- if .IsStreaming }}
        var payload {{ .PayloadType }}
        {{- if or .DefaultFields .RequiredFields .EnumFields }}
        fields, ferr := topLevelJSONFieldSet(p.Arguments)
        if ferr != nil {
            return false, goa.PermanentError("invalid_params", "%s", ferr.Error())
        }
        rawFields, err := decodeMCPPayloadFields(p.Arguments)
        if err != nil {
            return false, goa.PermanentError("invalid_params", "%s", err.Error())
        }
        {{- end }}
        if err := decodeMCPPayloadStrict(p.Arguments, &payload); err != nil {
            return false, goa.PermanentError("invalid_params", "%s", err.Error())
        }
        {{- if .DefaultFields }}
        {
            {{- range .DefaultFields }}
            if _, ok := fields[{{ printf "%q" .Name }}]; !ok {
            {{- if eq .Kind "string" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- else if eq .Kind "int" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- else if eq .Kind "bool" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- end }}
            }
            {{- end }}
        }
        {{- end }}
        {{- if .RequiredFields }}
        {
            {{- range .RequiredFields }}
            if err := validateMCPPayloadRequired(rawFields, {{ printf "%q" . }}); err != nil {
                return false, err
            }
            {{- end }}
        }
        {{- end }}
        {{- if .EnumFields }}
        {
            {{- range $field, $vals := .EnumFields }}
            if err := validateMCPPayloadEnum(rawFields, {{ printf "%q" $field }}, {{- range $idx, $val := $vals }}{{ if $idx }}, {{ end }}{{ printf "%q" $val }}{{- end }}); err != nil {
                return false, err
            }
            {{- end }}
        }
        {{- end }}
        bridge := &{{ goify .OriginalMethodName }}StreamBridge{ out: stream, adapter: a }
        if err := a.service.{{ .OriginalMethodName }}(ctx, payload, bridge); err != nil {
            return true, a.sendToolError(ctx, stream, p.Name, err)
        }
        return false, nil
        {{- else }}
        var payload {{ .PayloadType }}
        {{- if or .DefaultFields .RequiredFields .EnumFields }}
        fields, ferr := topLevelJSONFieldSet(p.Arguments)
        if ferr != nil {
            return false, goa.PermanentError("invalid_params", "%s", ferr.Error())
        }
        rawFields, err := decodeMCPPayloadFields(p.Arguments)
        if err != nil {
            return false, goa.PermanentError("invalid_params", "%s", err.Error())
        }
        {{- end }}
        if err := decodeMCPPayloadStrict(p.Arguments, &payload); err != nil {
            return false, goa.PermanentError("invalid_params", "%s", err.Error())
        }
        {{- if .DefaultFields }}
        {
            {{- range .DefaultFields }}
            if _, ok := fields[{{ printf "%q" .Name }}]; !ok {
            {{- if eq .Kind "string" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- else if eq .Kind "int" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- else if eq .Kind "bool" }}
                payload.{{ .GoName }} = {{ .Literal }}
            {{- end }}
            }
            {{- end }}
        }
        {{- end }}
        {{- if .RequiredFields }}
        {
            {{- range .RequiredFields }}
            if err := validateMCPPayloadRequired(rawFields, {{ printf "%q" . }}); err != nil {
                return false, err
            }
            {{- end }}
        }
        {{- end }}
        {{- if .EnumFields }}
        {
            {{- range $field, $vals := .EnumFields }}
            if err := validateMCPPayloadEnum(rawFields, {{ printf "%q" $field }}, {{- range $idx, $val := $vals }}{{ if $idx }}, {{ end }}{{ printf "%q" $val }}{{- end }}); err != nil {
                return false, err
            }
            {{- end }}
        }
        {{- end }}
        {{- end }}
        {{- end }}
        {{- if not .IsStreaming }}
        {{- if .HasResult }}
        {{- if .HasPayload }}
        result, err := a.service.{{ .OriginalMethodName }}(ctx, payload)
        {{- else }}
        result, err := a.service.{{ .OriginalMethodName }}(ctx)
        {{- end }}
        if err != nil {
            return true, a.sendToolError(ctx, stream, p.Name, err)
        }
        {{- if eq .ResultType "string" }}
        s := string(result)
        {{- else }}
        s, serr := mcpruntime.EncodeJSONToString(ctx, goahttp.ResponseEncoder, result)
        if serr != nil {
            return false, serr
        }
        {{- end }}
        final := &ToolsCallResult{
            Content: []*ContentItem{
                buildContentItem(a, s),
            },
        }
        a.log(ctx, "response", map[string]any{"method": "tools/call", "name": p.Name})
        return false, stream.SendAndClose(ctx, final)
        {{- else }}
        {{- if .HasPayload }}
        if err := a.service.{{ .OriginalMethodName }}(ctx, payload); err != nil {
            return true, a.sendToolError(ctx, stream, p.Name, err)
        }
        {{- else }}
        if err := a.service.{{ .OriginalMethodName }}(ctx); err != nil {
            return true, a.sendToolError(ctx, stream, p.Name, err)
        }
        {{- end }}
        ok := stringPtr("{\"status\":\"success\"}")
        a.log(ctx, "response", map[string]any{"method": "tools/call", "name": p.Name})
        return false, stream.SendAndClose(ctx, &ToolsCallResult{
            Content: []*ContentItem{
                &ContentItem{ Type: "text", Text: ok },
            },
        })
        {{- end }}
        {{- end }}
    {{- end }}
    default:
        return false, goa.PermanentError("method_not_found", "Unknown tool: %s", p.Name)
    }
}
{{- end }}


{{- end }}
