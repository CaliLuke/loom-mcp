{{ comment "MCPAdapter core: types, options, constructor, helpers" }}

type MCPAdapter struct {
    service {{ .Package }}.Service
    initialized bool
    initializedSessions map[string]struct{}
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
    return "goa-ai/mcp/{{ .MCPPackage }}"
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
        "goaai.mcp.calls",
        metric.WithUnit("{call}"),
        metric.WithDescription("Total MCP calls handled by the generated adapter."),
    )
    errorCounter, _ := meter.Int64Counter(
        "goaai.mcp.errors",
        metric.WithUnit("{call}"),
        metric.WithDescription("Total MCP calls handled by the generated adapter that resulted in an error."),
    )
    durationHistogram, _ := meter.Float64Histogram(
        "goaai.mcp.duration_ms",
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

    if streamErr, ok := toolStreamError(err); ok {
        a.log(ctx, "response", map[string]any{"method": "tools/call", "name": toolName, "is_error": true})
        return streamErr
    }

    mapped := a.mapError(err)
    if streamErr, ok := toolStreamError(mapped); ok {
        a.log(ctx, "response", map[string]any{"method": "tools/call", "name": toolName, "is_error": true})
        return streamErr
    }
    return mapped
}

func toolStreamError(err error) (error, bool) {
    if err == nil {
        return nil, false
    }

    status, ok := goa.ErrorStatusCode(err)
    if !ok {
        return nil, false
    }

    message := goa.ErrorSafeMessage(err)

    switch status {
    case http.StatusBadRequest:
        return goa.PermanentError("invalid_params", "%s", message), true
    case http.StatusNotFound:
        return goa.PermanentError("method_not_found", "%s", message), true
    default:
        return goa.PermanentError("internal_error", "%s", message), true
    }
}

// Initialize handles the MCP initialize request.
func (a *MCPAdapter) Initialize(ctx context.Context, p *InitializePayload) (res *InitializeResult, err error) {
    ctx, span, start, attrs := a.startTelemetry(ctx, "initialize")
    defer func() {
        a.finishTelemetry(ctx, span, start, attrs, err, false)
    }()
    if p == nil || p.ProtocolVersion == "" {
        return nil, goa.PermanentError("invalid_params", "Missing protocolVersion")
    }
    if !a.supportsProtocolVersion(p.ProtocolVersion) {
        return nil, goa.PermanentError("invalid_params", "Unsupported protocol version")
    }
    requestSessionID := mcpruntime.SessionIDFromContext(ctx)
    sessionID := requestSessionID
    if sessionID == "" && mcpruntime.ResponseWriterFromContext(ctx) != nil {
        sessionID = mcpruntime.EnsureSessionID(ctx)
    }

    a.mu.Lock()
    if sessionID == "" {
        if a.initialized {
            a.mu.Unlock()
            return nil, goa.PermanentError("invalid_params", "Already initialized")
        }
        a.initialized = true
    } else {
        if _, ok := a.initializedSessions[sessionID]; ok {
            a.mu.Unlock()
            return nil, goa.PermanentError("invalid_params", "Already initialized")
        }
        a.initializedSessions[sessionID] = struct{}{}
    }
    a.mu.Unlock()

    serverInfo := &ServerInfo{
        Name:    {{ quote .MCPName }},
        Version: {{ quote .MCPVersion }},
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

    return &InitializeResult{
        ProtocolVersion: a.mcpProtocolVersion(),
        ServerInfo:      serverInfo,
        Capabilities:    capabilities,
    }, nil
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
