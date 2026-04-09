{{ comment "MCPAdapter core: types, options, constructor, helpers" }}

type MCPAdapter struct {
    service {{ .Package }}.Service
    initialized bool
    initializedSessions map[string]struct{}
    sessionPrincipals map[string]string
    mu sync.RWMutex
    opts *MCPAdapterOptions
    tracer trace.Tracer
    callCounter metric.Int64Counter
    errorCounter metric.Int64Counter
    durationHistogram metric.Float64Histogram
    {{- if or .StaticPrompts .DynamicPrompts }}
    promptProvider PromptProvider
    {{- end }}
    // Minimal subscription registry keyed by resource URI
    subs   map[string]int
    subsMu sync.Mutex
    // Broadcaster for server-initiated events (notifications/resources)
    broadcaster mcpruntime.Broadcaster
    // resourceNameToURI holds DSL-derived mapping for policy and lookups
    resourceNameToURI map[string]string
}

type (
    // ToolCallInterceptorInfo describes a generated MCP tools/call invocation.
    ToolCallInterceptorInfo interface {
        goa.InterceptorInfo
        Tool() string
        RawArguments() json.RawMessage
    }

    // ToolCallHandler is the generated MCP tool-call dispatcher.
    ToolCallHandler func(ctx context.Context, payload *ToolsCallPayload, stream ToolsCallServerStream) (bool, error)

    // ToolCallInterceptor wraps generated MCP tool execution.
    ToolCallInterceptor func(ctx context.Context, info ToolCallInterceptorInfo, payload *ToolsCallPayload, stream ToolsCallServerStream, next ToolCallHandler) (bool, error)
)

type toolCallInterceptorInfo struct {
    service     string
    method      string
    tool        string
    rawPayload  any
    rawArgs     json.RawMessage
}

func (i *toolCallInterceptorInfo) Service() string                 { return i.service }
func (i *toolCallInterceptorInfo) Method() string                  { return i.method }
func (i *toolCallInterceptorInfo) CallType() goa.InterceptorCallType { return goa.InterceptorUnary }
func (i *toolCallInterceptorInfo) RawPayload() any                 { return i.rawPayload }
func (i *toolCallInterceptorInfo) Tool() string                    { return i.tool }
func (i *toolCallInterceptorInfo) RawArguments() json.RawMessage   { return i.rawArgs }

// MCPAdapterOptions allows customizing adapter behavior.
type MCPAdapterOptions struct {
    // Logger is an optional hook called with internal adapter events.
    Logger func(ctx context.Context, event string, details any)
    // ErrorMapper allows mapping arbitrary errors to framework-friendly errors
    ErrorMapper func(error) error
    // ToolCallInterceptors wrap generated tools/call execution.
    ToolCallInterceptors []ToolCallInterceptor
    // TelemetryName overrides the instrumentation scope used for OpenTelemetry spans and metrics.
    TelemetryName string
    // Tracer overrides the tracer used by the generated MCP adapter.
    Tracer trace.Tracer
    // Meter overrides the meter used by the generated MCP adapter.
    Meter metric.Meter
    // Allowed/Deny lists for resource URIs; Denied takes precedence unless header allow overrides
    AllowedResourceURIs []string
    DeniedResourceURIs  []string
    // Name-based policy resolved to URIs at construction
    AllowedResourceNames []string
    DeniedResourceNames  []string
    StructuredStreamJSON bool
    ProtocolVersionOverride string
    // SessionPrincipal extracts a stable auth/session owner identity from ctx.
    SessionPrincipal func(context.Context) string
    // Pluggable broadcaster, else default channel broadcaster
    Broadcaster mcpruntime.Broadcaster
    BroadcastBuffer int
    DropIfSlow bool
}

func NewMCPAdapter(service {{ .Package }}.Service{{ if or .StaticPrompts .DynamicPrompts }}, promptProvider PromptProvider{{ end }}, opts *MCPAdapterOptions) *MCPAdapter {
    // Resolve name-based policy to URIs
    if opts != nil && (len(opts.AllowedResourceNames) > 0 || len(opts.DeniedResourceNames) > 0) {
        nameToURI := map[string]string{
            {{- range .Resources }}
            {{ printf "%q" .Name }}: {{ printf "%q" .URI }},
            {{- end }}
        }
        seen := map[string]struct{}{}
        for _, n := range opts.AllowedResourceNames {
            if u, ok := nameToURI[n]; ok {
                if _, dup := seen["allow:"+u]; !dup {
                    opts.AllowedResourceURIs = append(opts.AllowedResourceURIs, u)
                    seen["allow:"+u] = struct{}{}
                }
            }
        }
        for _, n := range opts.DeniedResourceNames {
            if u, ok := nameToURI[n]; ok {
                if _, dup := seen["deny:"+u]; !dup {
                    opts.DeniedResourceURIs = append(opts.DeniedResourceURIs, u)
                    seen["deny:"+u] = struct{}{}
                }
            }
        }
    }
    // Broadcaster
    var bc mcpruntime.Broadcaster
    if opts != nil && opts.Broadcaster != nil {
        bc = opts.Broadcaster
    } else {
        buf := 32
        drop := true
        if opts != nil {
            if opts.BroadcastBuffer > 0 {
                buf = opts.BroadcastBuffer
            }
            if opts.DropIfSlow == false {
                drop = false
            }
        }
        bc = mcpruntime.NewChannelBroadcaster(buf, drop)
    }
    telemetryName := defaultMCPAdapterTelemetryName(opts)
    tracer := defaultMCPAdapterTracer(opts, telemetryName)
    callCounter, errorCounter, durationHistogram := defaultMCPAdapterMetrics(opts, telemetryName)
    // Build name->URI map from generated resources
    nameToURI := map[string]string{
        {{- range .Resources }}
        {{ printf "%q" .Name }}: {{ printf "%q" .URI }},
        {{- end }}
    }
    return &MCPAdapter{
        service: service,
        initializedSessions: make(map[string]struct{}),
        sessionPrincipals: make(map[string]string),
        opts: opts,
        tracer: tracer,
        callCounter: callCounter,
        errorCounter: errorCounter,
        durationHistogram: durationHistogram,
        {{- if or .StaticPrompts .DynamicPrompts }}
        promptProvider: promptProvider,
        {{- end }}
        subs: make(map[string]int),
        broadcaster: bc,
        resourceNameToURI: nameToURI,
    }
}

// mcpProtocolVersion resolves the protocol version from options or default.
func (a *MCPAdapter) mcpProtocolVersion() string {
    if a != nil && a.opts != nil && a.opts.ProtocolVersionOverride != "" {
        return a.opts.ProtocolVersionOverride
    }
    return DefaultProtocolVersion
}

func (a *MCPAdapter) supportsProtocolVersion(requested string) bool {
    base := a.mcpProtocolVersion()
    if requested == base {
        return true
    }
    if !validMCPProtocolVersionDate(requested) || !validMCPProtocolVersionDate(base) {
        return false
    }
    return requested >= base
}

func validMCPProtocolVersionDate(v string) bool {
    if len(v) != 10 {
        return false
    }
    for i := range v {
        switch i {
        case 4, 7:
            if v[i] != '-' {
                return false
            }
        default:
            if v[i] < '0' || v[i] > '9' {
                return false
            }
        }
    }
    return true
}

// parseQueryParamsToJSON converts URI query params into JSON.
func parseQueryParamsToJSON(uri string) ([]byte, error) {
    u, err := url.Parse(uri)
    if err != nil {
        return nil, fmt.Errorf("invalid resource URI: %w", err)
    }
    q := u.Query()
    if len(q) == 0 {
        return []byte("{}"), nil
    }
    // Copy to plain map[string][]string to avoid depending on url.Values in helper
    m := make(map[string][]string, len(q))
    for k, v := range q { m[k] = v }
    coerced := mcpruntime.CoerceQuery(m)
    return json.Marshal(coerced)
}

func (a *MCPAdapter) isInitialized(ctx context.Context) bool {
    a.mu.RLock()
    defer a.mu.RUnlock()
    if sessionID := mcpruntime.SessionIDFromContext(ctx); sessionID != "" {
        _, ok := a.initializedSessions[sessionID]
        return ok
    }
    return a.initialized
}

func (a *MCPAdapter) markInitializedSession(sessionID string) {
    a.mu.Lock()
    defer a.mu.Unlock()
    if sessionID == "" {
        a.initialized = true
        return
    }
    a.initializedSessions[sessionID] = struct{}{}
}

func (a *MCPAdapter) captureSessionPrincipal(ctx context.Context, sessionID string) {
    if a == nil || sessionID == "" {
        return
    }
    principal := a.sessionPrincipal(ctx)
    if principal == "" {
        return
    }
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.sessionPrincipals == nil {
        a.sessionPrincipals = make(map[string]string)
    }
    if existing := strings.TrimSpace(a.sessionPrincipals[sessionID]); existing != "" {
        return
    }
    a.sessionPrincipals[sessionID] = principal
}

func (a *MCPAdapter) clearSessionPrincipal(sessionID string) {
    if a == nil || sessionID == "" {
        return
    }
    a.mu.Lock()
    defer a.mu.Unlock()
    delete(a.sessionPrincipals, sessionID)
}

func (a *MCPAdapter) assertSessionPrincipal(ctx context.Context, sessionID string) error {
    if a == nil || sessionID == "" {
        return nil
    }
    a.mu.RLock()
    expected := strings.TrimSpace(a.sessionPrincipals[sessionID])
    a.mu.RUnlock()
    if expected == "" {
        return nil
    }
    actual := a.sessionPrincipal(ctx)
    if actual == "" || actual != expected {
        return errors.New("session user mismatch")
    }
    return nil
}

func (a *MCPAdapter) sessionPrincipal(ctx context.Context) string {
    if a != nil && a.opts != nil && a.opts.SessionPrincipal != nil {
        return strings.TrimSpace(a.opts.SessionPrincipal(ctx))
    }
    if tokenInfo := mcpauth.TokenInfoFromContext(ctx); tokenInfo != nil {
        return strings.TrimSpace(tokenInfo.UserID)
    }
    return ""
}

func (a *MCPAdapter) log(ctx context.Context, event string, details any) {
    if a != nil && a.opts != nil && a.opts.Logger != nil {
        a.opts.Logger(ctx, event, details)
    }
}

func (a *MCPAdapter) mapError(err error) error {
    if a != nil && a.opts != nil && a.opts.ErrorMapper != nil && err != nil {
        if m := a.opts.ErrorMapper(err); m != nil {
            return m
        }
    }
    return err
}

func (a *MCPAdapter) toolCallInfo(p *ToolsCallPayload) ToolCallInterceptorInfo {
    info := &toolCallInterceptorInfo{
        service:    "{{ .ServiceName }}",
        method:     "tools/call",
        rawPayload: p,
    }
    if p != nil {
        info.tool = p.Name
        info.rawArgs = p.Arguments
    }
    return info
}

func (a *MCPAdapter) wrapToolCallHandler(info ToolCallInterceptorInfo, next ToolCallHandler) ToolCallHandler {
    if a == nil || a.opts == nil || len(a.opts.ToolCallInterceptors) == 0 {
        return next
    }
    wrapped := next
    for i := len(a.opts.ToolCallInterceptors) - 1; i >= 0; i-- {
        interceptor := a.opts.ToolCallInterceptors[i]
        if interceptor == nil {
            continue
        }
        currentNext := wrapped
        wrapped = func(ctx context.Context, payload *ToolsCallPayload, stream ToolsCallServerStream) (bool, error) {
            return interceptor(ctx, info, payload, stream, currentNext)
        }
    }
    return wrapped
}

func defaultMCPAdapterTelemetryName(opts *MCPAdapterOptions) string {
    if opts != nil && opts.TelemetryName != "" {
        return opts.TelemetryName
    }
    return "loom-mcp/mcp/{{ .MCPPackage }}"
}

func defaultMCPAdapterTracer(opts *MCPAdapterOptions, name string) trace.Tracer {
    if opts != nil && opts.Tracer != nil {
        return opts.Tracer
    }
    return otel.Tracer(name)
}

func defaultMCPAdapterMetrics(opts *MCPAdapterOptions, name string) (metric.Int64Counter, metric.Int64Counter, metric.Float64Histogram) {
    var meter metric.Meter
    if opts != nil && opts.Meter != nil {
        meter = opts.Meter
    } else {
        meter = otel.Meter(name)
    }
    callCounter, _ := meter.Int64Counter(
        "loom_mcp.mcp.calls",
        metric.WithUnit("{call}"),
        metric.WithDescription("Total MCP calls handled by the generated adapter."),
    )
    errorCounter, _ := meter.Int64Counter(
        "loom_mcp.mcp.errors",
        metric.WithUnit("{call}"),
        metric.WithDescription("Total MCP calls handled by the generated adapter that resulted in an error."),
    )
    durationHistogram, _ := meter.Float64Histogram(
        "loom_mcp.mcp.duration_ms",
        metric.WithUnit("ms"),
        metric.WithDescription("Duration of MCP calls handled by the generated adapter in milliseconds."),
    )
    return callCounter, errorCounter, durationHistogram
}

func (a *MCPAdapter) startTelemetry(ctx context.Context, method string, attrs ...attribute.KeyValue) (context.Context, trace.Span, time.Time, []attribute.KeyValue) {
    baseAttrs := append([]attribute.KeyValue{
        attribute.String("rpc.system", "mcp"),
        attribute.String("rpc.method", method),
        attribute.String("mcp.service", "{{ .ServiceName }}"),
    }, attrs...)
    tracer := a.tracer
    if tracer == nil {
        tracer = otel.Tracer(defaultMCPAdapterTelemetryName(a.opts))
    }
    ctx, span := tracer.Start(ctx, "mcp."+method)
    span.SetAttributes(baseAttrs...)
    return ctx, span, time.Now(), baseAttrs
}

func (a *MCPAdapter) finishTelemetry(ctx context.Context, span trace.Span, start time.Time, attrs []attribute.KeyValue, err error, toolErr bool) {
    duration := time.Since(start)
    statusClass := "ok"
    if err != nil || toolErr {
        statusClass = "error"
    }
    metricAttrs := append([]attribute.KeyValue{}, attrs...)
    metricAttrs = append(metricAttrs,
        attribute.String("status_class", statusClass),
        attribute.Bool("mcp.tool_error", toolErr),
    )
    if a.callCounter != nil {
        a.callCounter.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
    }
    if a.durationHistogram != nil {
        a.durationHistogram.Record(ctx, float64(duration.Microseconds())/1000.0, metric.WithAttributes(metricAttrs...))
    }
    if (err != nil || toolErr) && a.errorCounter != nil {
        a.errorCounter.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
    }
    span.SetAttributes(
        attribute.String("status_class", statusClass),
        attribute.Bool("mcp.tool_error", toolErr),
        attribute.Int64("mcp.duration_ms", duration.Milliseconds()),
    )
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
    } else if toolErr {
        span.SetStatus(codes.Error, "tool returned MCP error result")
    } else {
        span.SetStatus(codes.Ok, "")
    }
    span.End()
}

func stringPtr(s string) *string {
    return &s
}

func isLikelyJSON(s string) bool {
    return json.Valid([]byte(s))
}

// buildContentItem returns a ContentItem honoring StructuredStreamJSON option.
func buildContentItem(a *MCPAdapter, s string) *ContentItem {
    if a != nil && a.opts != nil && a.opts.StructuredStreamJSON && isLikelyJSON(s) {
        mt := stringPtr("application/json")
        return &ContentItem{
            Type:     "text",
            MimeType: mt,
            Text:     &s,
        }
    }
    return &ContentItem{
        Type: "text",
        Text: &s,
    }
}

func (a *MCPAdapter) sendToolError(ctx context.Context, stream ToolsCallServerStream, toolName string, err error) error {
    if err == nil {
        return nil
    }

    mapped := a.mapError(err)
    if mapped == nil {
        mapped = err
    }
    isError := true
    result := &ToolsCallResult{
        Content: []*ContentItem{
            buildContentItem(a, formatToolErrorText(mapped)),
        },
        IsError: &isError,
    }
    a.log(ctx, "response", map[string]any{"method": "tools/call", "name": toolName, "is_error": true})
    return stream.SendAndClose(ctx, result)
}

func formatToolErrorText(err error) string {
    if err == nil {
        return "[internal_error] Tool execution failed."
    }

    code := strings.TrimSpace(goa.ErrorRemedyCode(err))
    if code == "" {
        var namer goa.LoomErrorNamer
        if errors.As(err, &namer) {
            code = strings.TrimSpace(namer.LoomErrorName())
        }
    }
    if code == "" {
        if status, ok := goa.ErrorStatusCode(err); ok {
            switch status {
            case http.StatusBadRequest:
                code = "invalid_params"
            case http.StatusNotFound:
                code = "not_found"
            default:
                code = "internal_error"
            }
        }
    }
    if code == "" {
        code = "internal_error"
    }

    message := strings.TrimSpace(goa.ErrorSafeMessage(err))
    if message == "" {
        message = "Tool execution failed."
    }
    recovery := strings.TrimSpace(goa.ErrorRetryHint(err))
    if recovery == "" {
        return fmt.Sprintf("[%s] %s", code, message)
    }
    return fmt.Sprintf("[%s] %s\nRecovery: %s", code, message, recovery)
}

func toolCallError(err error, defaultCode string, defaultRecovery string) error {
    if err == nil {
        err = goa.PermanentError(defaultCode, "Tool execution failed.")
    }
    code := strings.TrimSpace(goa.ErrorRemedyCode(err))
    if code == "" {
        code = defaultCode
    }
    message := strings.TrimSpace(goa.ErrorSafeMessage(err))
    if message == "" {
        message = "Tool execution failed."
    }
    recovery := strings.TrimSpace(goa.ErrorRetryHint(err))
    if recovery == "" {
        recovery = defaultRecovery
    }
	return goa.WithErrorRemedy(goa.PermanentError(code, "%s", message), &goa.ErrorRemedy{
		Code:        code,
		SafeMessage: message,
		RetryHint:   recovery,
	})
}

func toolInputError(err error, raw json.RawMessage) error {
	return toolCallError(err, "invalid_params", inferToolInputRecovery(err, raw))
}

func inferToolInputRecovery(err error, raw json.RawMessage) string {
	message := strings.TrimSpace(goa.ErrorSafeMessage(err))
	if message == "" {
		message = strings.TrimSpace(err.Error())
	}
	if field := missingFieldFromMessage(message); field != "" {
		return fmt.Sprintf("Include required field %q.", field)
	}
	if action, ok := actionValueEnvelopeExample(raw); ok {
		return fmt.Sprintf("Include the nested value object. Example: %s", action)
	}
	if strings.Contains(message, "unexpected end of JSON input") || strings.Contains(message, "unexpected EOF") {
		return "Provide complete JSON arguments. If a field expects an object, include {} instead of leaving it incomplete."
	}
	return "Provide valid tool arguments."
}

func missingFieldFromMessage(message string) string {
	const prefix = "Missing required field: "
	if !strings.HasPrefix(message, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(message, prefix))
}

func actionValueEnvelopeExample(raw json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", false
	}
	if action, ok := actionValueExampleForObject(fields); ok {
		return action, true
	}
	for name, nestedRaw := range fields {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(nestedRaw, &nested); err != nil {
			continue
		}
		if example, ok := actionValueExampleForObject(nested); ok {
			return fmt.Sprintf(`{"%s":%s}`, name, example), true
		}
	}
	return "", false
}

func actionValueExampleForObject(fields map[string]json.RawMessage) (string, bool) {
	actionRaw, hasAction := fields["action"]
	if !hasAction {
		return "", false
	}
	if _, hasValue := fields["value"]; hasValue {
		return "", false
	}
	var action string
	if err := json.Unmarshal(actionRaw, &action); err != nil {
		return "", false
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return "", false
	}
	return fmt.Sprintf(`{"action":%q,"value":{}}`, action), true
}

func formatToolSuccessText(v any) string {
    switch value := v.(type) {
    case nil:
        return "OK"
    case string:
        if strings.TrimSpace(value) == "" {
            return "OK"
        }
        return value
    case *string:
        if value == nil || strings.TrimSpace(*value) == "" {
            return "OK"
        }
        return *value
    case bool:
        if value {
            return "true"
        }
        return "false"
    case *bool:
        if value == nil {
            return "OK"
        }
        if *value {
            return "true"
        }
        return "false"
    }

    normalized, ok := normalizeToolSuccessValue(v)
    if !ok {
        return fmt.Sprint(v)
    }
    return summarizeToolSuccessValue(normalized)
}

func normalizeToolSuccessValue(v any) (any, bool) {
    if v == nil {
        return nil, false
    }
    raw, err := json.Marshal(v)
    if err != nil {
        return nil, false
    }
    var normalized any
    if err := json.Unmarshal(raw, &normalized); err != nil {
        return nil, false
    }
    return normalized, true
}

func summarizeToolSuccessValue(v any) string {
    switch value := v.(type) {
    case nil:
        return "OK"
    case string:
        if strings.TrimSpace(value) == "" {
            return "OK"
        }
        return value
    case bool:
        if value {
            return "true"
        }
        return "false"
    case float64:
        return strconv.FormatFloat(value, 'f', -1, 64)
    case []any:
        return summarizeToolSuccessList(value)
    case map[string]any:
        return summarizeToolSuccessMap(value)
    default:
        return fmt.Sprint(value)
    }
}

func summarizeToolSuccessList(items []any) string {
    if len(items) == 0 {
        return "No items."
    }
    parts := make([]string, 0, min(len(items), 5))
    for _, item := range items {
        part := strings.TrimSpace(summarizeToolSuccessValue(item))
        if part == "" {
            continue
        }
        parts = append(parts, part)
        if len(parts) == 5 {
            break
        }
    }
    if len(parts) == 0 {
        return fmt.Sprintf("%d items.", len(items))
    }
    if len(items) > len(parts) {
        parts = append(parts, fmt.Sprintf("... (%d total)", len(items)))
    }
    return strings.Join(parts, "\n")
}

func summarizeToolSuccessMap(fields map[string]any) string {
    preferredScalars := []string{"result", "output", "summary", "message", "value", "name", "ack", "sentiment", "status"}
    for _, key := range preferredScalars {
        if scalar, ok := scalarToolSuccessText(fields[key]); ok {
            return scalar
        }
    }
    preferredLists := []string{"items", "results", "keywords", "templates", "documents"}
    for _, key := range preferredLists {
        if list, ok := fields[key].([]any); ok {
            return summarizeToolSuccessList(list)
        }
    }
    if len(fields) == 1 {
        for _, value := range fields {
            return summarizeToolSuccessValue(value)
        }
    }
    if name, ok := scalarToolSuccessText(fields["name"]); ok {
        if version, ok := scalarToolSuccessText(fields["version"]); ok {
            return strings.TrimSpace(name + " " + version)
        }
    }
    keys := make([]string, 0, len(fields))
    for key := range fields {
        keys = append(keys, key)
    }
    sort.Strings(keys)
    if len(keys) > 4 {
        keys = keys[:4]
    }
    return fmt.Sprintf("Fields: %s", strings.Join(keys, ", "))
}

func scalarToolSuccessText(v any) (string, bool) {
    switch value := v.(type) {
    case string:
        trimmed := strings.TrimSpace(value)
        return trimmed, trimmed != ""
    case bool:
        if value {
            return "true", true
        }
        return "false", true
    case float64:
        return strconv.FormatFloat(value, 'f', -1, 64), true
    default:
        return "", false
    }
}

// Initialize handles the MCP initialize request.
func (a *MCPAdapter) Initialize(ctx context.Context, p *InitializePayload) (res *InitializeResult, err error) {
    ctx, span, start, attrs := a.startTelemetry(ctx, "initialize")
    defer func() {
        a.finishTelemetry(ctx, span, start, attrs, err, false)
    }()
    requestProtocol := ""
    requestSessionID := mcpruntime.SessionIDFromContext(ctx)
    if p != nil {
        requestProtocol = p.ProtocolVersion
    }
    a.log(ctx, "request", map[string]any{
        "method": "initialize",
        "session_id": requestSessionID,
        "protocol_version": requestProtocol,
    })
    if p == nil || p.ProtocolVersion == "" {
        return nil, goa.PermanentError("invalid_params", "Missing protocolVersion")
    }
    if !a.supportsProtocolVersion(p.ProtocolVersion) {
        return nil, goa.PermanentError("invalid_params", "Unsupported protocol version")
    }
    sessionID := requestSessionID
    if sessionID == "" && mcpruntime.ResponseWriterFromContext(ctx) != nil {
        sessionID = mcpruntime.EnsureSessionID(ctx)
    }

    a.mu.Lock()
    if sessionID == "" {
        if a.initialized {
            a.mu.Unlock()
            err = goa.PermanentError("invalid_params", "Already initialized")
            a.log(ctx, "response", map[string]any{
                "method": "initialize",
                "session_id": sessionID,
                "protocol_version": p.ProtocolVersion,
                "error": err.Error(),
            })
            return nil, err
        }
        a.initialized = true
    } else {
        if _, ok := a.initializedSessions[sessionID]; ok {
            a.mu.Unlock()
            err = goa.PermanentError("invalid_params", "Already initialized")
            a.log(ctx, "response", map[string]any{
                "method": "initialize",
                "session_id": sessionID,
                "protocol_version": p.ProtocolVersion,
                "error": err.Error(),
            })
            return nil, err
        }
        a.initializedSessions[sessionID] = struct{}{}
    }
    a.mu.Unlock()

    a.captureSessionPrincipal(ctx, sessionID)

    serverInfo := &ServerInfo{
        Name:    {{ quote .MCPName }},
        Version: {{ quote .MCPVersion }},
        {{- if .WebsiteURL }}
        WebsiteURL: stringPtr({{ quote .WebsiteURL }}),
        {{- end }}
        {{- if .Icons }}
        Icons: []*Icon{
            {{- range .Icons }}
            {
                Src: {{ quote .Source }},
                {{- if .MIMEType }}
                MimeType: stringPtr({{ quote .MIMEType }}),
                {{- end }}
                {{- if .Sizes }}
                Sizes: []string{
                    {{- range .Sizes }}
                    {{ quote . }},
                    {{- end }}
                },
                {{- end }}
                {{- if .Theme }}
                Theme: stringPtr({{ quote .Theme }}),
                {{- end }}
            },
            {{- end }}
        },
        {{- end }}
    }

    capabilities := &ServerCapabilities{}
    {{- if .Tools }}
    capabilities.Tools = &ToolsCapability{}
    {{- end }}
    {{- if .Resources }}
    capabilities.Resources = &ResourcesCapability{}
    {{- end }}
    {{- if or .StaticPrompts .DynamicPrompts }}
    capabilities.Prompts = &PromptsCapability{}
    {{- end }}

    res = &InitializeResult{
        ProtocolVersion: a.mcpProtocolVersion(),
        ServerInfo:      serverInfo,
        Capabilities:    capabilities,
    }
    a.log(ctx, "response", map[string]any{
        "method": "initialize",
        "session_id": sessionID,
        "protocol_version": res.ProtocolVersion,
        "server_name": serverInfo.Name,
    })
    return res, nil
}

// Ping handles the MCP ping request.
func (a *MCPAdapter) Ping(ctx context.Context) (res *PingResult, err error) {
    ctx, span, start, attrs := a.startTelemetry(ctx, "ping")
    defer func() {
        a.finishTelemetry(ctx, span, start, attrs, err, false)
    }()
    a.log(ctx, "request", map[string]any{"method": "ping"})
    res = &PingResult{Pong: true}
    a.log(ctx, "response", map[string]any{"method": "ping"})
    return res, nil
}
