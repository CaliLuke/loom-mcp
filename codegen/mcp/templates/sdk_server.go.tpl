// SDK-backed MCP streamable HTTP server.

type SDKServer struct {
	Handler http.Handler
	Adapter *MCPAdapter
	Server  *mcpsdk.Server
}

type SDKServerOptions struct {
	Adapter        *MCPAdapterOptions
	RequestContext func(context.Context, *http.Request) context.Context
	{{- if or .StaticPrompts .DynamicPrompts }}
	PromptProvider PromptProvider
	{{- end }}
	Server         *mcpsdk.ServerOptions
	StreamableHTTP *mcpsdk.StreamableHTTPOptions
}

func NewSDKServer(service {{ .Package }}.Service, opts *SDKServerOptions) (*SDKServer, error) {
	var adapterOpts *MCPAdapterOptions
	var requestContext func(context.Context, *http.Request) context.Context
	{{- if or .StaticPrompts .DynamicPrompts }}
	var promptProvider PromptProvider
	{{- end }}
	var serverOpts *mcpsdk.ServerOptions
	var streamableOpts *mcpsdk.StreamableHTTPOptions
	if opts != nil {
		adapterOpts = opts.Adapter
		requestContext = opts.RequestContext
		{{- if or .StaticPrompts .DynamicPrompts }}
		promptProvider = opts.PromptProvider
		{{- end }}
		serverOpts = opts.Server
		streamableOpts = opts.StreamableHTTP
	}

	adapter := NewMCPAdapter(service{{ if or .StaticPrompts .DynamicPrompts }}, promptProvider{{ end }}, adapterOpts)

	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    {{ quote .MCPName }},
		Version: {{ quote .MCPVersion }},
	}, serverOpts)
	if err := registerSDKTools(server, adapter, requestContext); err != nil {
		return nil, err
	}
	if err := registerSDKResources(server, adapter, requestContext); err != nil {
		return nil, err
	}
	if err := registerSDKPrompts(server, adapter, requestContext); err != nil {
		return nil, err
	}

	return &SDKServer{
		Handler: newSDKHandler(server, adapter, requestContext, streamableOpts),
		Adapter: adapter,
		Server:  server,
	}, nil
}

type sdkResponseObserver struct {
	http.ResponseWriter
	statusCode int
}

func (w *sdkResponseObserver) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *sdkResponseObserver) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func newSDKHandler(server *mcpsdk.Server, adapter *MCPAdapter, requestContext func(context.Context, *http.Request) context.Context, streamableOpts *mcpsdk.StreamableHTTPOptions) http.Handler {
	base := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, streamableOpts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestContext != nil {
			r = r.WithContext(requestContext(r.Context(), r))
		}
		if r.Method == http.MethodGet {
			serveSDKEventsStream(server, adapter, w, r)
			return
		}
		observer := &sdkResponseObserver{ResponseWriter: w}
		base.ServeHTTP(observer, r)
		if sessionID := observer.Header().Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
			adapter.captureSessionPrincipal(r.Context(), sessionID)
		}
	})
}

func serveSDKEventsStream(server *mcpsdk.Server, adapter *MCPAdapter, w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	adapter.log(r.Context(), "events_stream_open", map[string]any{
		"session_id": sessionID,
		"has_accept": strings.TrimSpace(r.Header.Get("Accept")) != "",
		"accept":     r.Header.Get("Accept"),
	})
	if sessionID == "" {
		adapter.log(r.Context(), "events_stream_rejected", map[string]any{
			"reason": "missing_session_id",
		})
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}
	if sdkSessionByID(server, sessionID) == nil {
		adapter.clearSessionPrincipal(sessionID)
		adapter.log(r.Context(), "events_stream_rejected", map[string]any{
			"session_id": sessionID,
			"reason":     "session_not_found",
		})
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err := adapter.assertSessionPrincipal(r.Context(), sessionID); err != nil {
		adapter.log(r.Context(), "events_stream_rejected", map[string]any{
			"session_id": sessionID,
			"reason":     "session_principal_mismatch",
			"error":      err.Error(),
		})
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	adapter.markInitializedSession(sessionID)
	adapter.captureSessionPrincipal(r.Context(), sessionID)
	sub, err := adapter.broadcaster.Subscribe(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to subscribe to events: %v", err), http.StatusInternalServerError)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	adapter.log(r.Context(), "events_stream_connected", map[string]any{
		"session_id": sessionID,
		"flushed":    flusher != nil,
	})
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			adapter.log(r.Context(), "events_stream_closed", map[string]any{
				"session_id": sessionID,
				"reason":     "request_context_done",
			})
			return
		case <-ticker.C:
			if sdkSessionByID(server, sessionID) == nil {
				adapter.clearSessionPrincipal(sessionID)
				adapter.log(r.Context(), "events_stream_closed", map[string]any{
					"session_id": sessionID,
					"reason":     "session_not_found",
				})
				return
			}
		case ev, ok := <-sub.C():
			if !ok {
				adapter.log(r.Context(), "events_stream_closed", map[string]any{
					"session_id": sessionID,
					"reason":     "broadcaster_closed",
				})
				return
			}
			res, ok := ev.(*EventsStreamResult)
			if !ok {
				adapter.log(r.Context(), "events_stream_skipped_event", map[string]any{
					"session_id": sessionID,
				})
				continue
			}
			if err := writeSDKNotificationEvent(w, "events/stream", sdkEventsStreamParams(res)); err != nil {
				adapter.log(r.Context(), "events_stream_closed", map[string]any{
					"session_id": sessionID,
					"reason":     "write_error",
					"error":      err.Error(),
				})
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func sdkSessionByID(server *mcpsdk.Server, sessionID string) *mcpsdk.ServerSession {
	if server == nil || sessionID == "" {
		return nil
	}
	for session := range server.Sessions() {
		if session != nil && session.ID() == sessionID {
			return session
		}
	}
	return nil
}

func writeSDKNotificationEvent(w http.ResponseWriter, method string, params any) error {
	message, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: notification\ndata: %s\n\n", message); err != nil {
		return err
	}
	return nil
}

func sdkEventsStreamParams(res *EventsStreamResult) map[string]any {
	params := map[string]any{
		"content": []map[string]any{},
	}
	if res == nil {
		return params
	}
	if res.IsError != nil {
		params["isError"] = *res.IsError
	}
	if len(res.Content) == 0 {
		return params
	}
	content := make([]map[string]any, 0, len(res.Content))
	for _, item := range res.Content {
		if item == nil {
			content = append(content, nil)
			continue
		}
		entry := map[string]any{
			"type": item.Type,
		}
		if item.Text != nil {
			entry["text"] = *item.Text
		}
		if item.Data != nil {
			entry["data"] = *item.Data
		}
		if item.MimeType != nil {
			entry["mimeType"] = *item.MimeType
		}
		if item.URI != nil {
			entry["uri"] = *item.URI
		}
		content = append(content, entry)
	}
	params["content"] = content
	return params
}

func registerSDKTools(server *mcpsdk.Server, adapter *MCPAdapter, requestContext func(context.Context, *http.Request) context.Context) error {
	{{- range .Tools }}
	{{- if .AnnotationsJSON }}
	annotations{{ goify .Name }}, err := sdkToolAnnotations(json.RawMessage([]byte({{ quote .AnnotationsJSON }})))
	if err != nil {
		return fmt.Errorf("tool %q annotations: %w", {{ quote .Name }}, err)
	}
	{{- end }}
	server.AddTool(&mcpsdk.Tool{
		Name:        {{ quote .Name }},
		Description: {{ quote .Description }},
		InputSchema: sdkToolInputSchema({{ quote .InputSchema }}),
		{{- if .AnnotationsJSON }}
		Annotations: annotations{{ goify .Name }},
		{{- end }}
	}, adapter.sdkToolHandler(requestContext))
	{{- end }}
	return nil
}

{{- if .Resources }}
func registerSDKResources(server *mcpsdk.Server, adapter *MCPAdapter, requestContext func(context.Context, *http.Request) context.Context) error {
	{{- range .Resources }}
	server.AddResource(&mcpsdk.Resource{
		Name:        {{ quote .Name }},
		URI:         {{ quote .URI }},
		Description: {{ quote .Description }},
		MIMEType:    {{ quote .MimeType }},
	}, adapter.sdkResourceHandler(requestContext))
	{{- end }}
	return nil
}
{{- else }}
func registerSDKResources(_ *mcpsdk.Server, _ *MCPAdapter, _ func(context.Context, *http.Request) context.Context) error {
	return nil
}
{{- end }}

{{- if or .StaticPrompts .DynamicPrompts }}
func registerSDKPrompts(server *mcpsdk.Server, adapter *MCPAdapter, requestContext func(context.Context, *http.Request) context.Context) error {
	{{- range .StaticPrompts }}
	server.AddPrompt(&mcpsdk.Prompt{
		Name:        {{ quote .Name }},
		Description: {{ quote .Description }},
	}, adapter.sdkPromptHandler(requestContext))
	{{- end }}
	{{- range .DynamicPrompts }}
	server.AddPrompt(&mcpsdk.Prompt{
		Name:        {{ quote .Name }},
		Description: {{ quote .Description }},
		Arguments: []*mcpsdk.PromptArgument{
			{{- range .Arguments }}
			{
				Name:        {{ quote .Name }},
				Description: {{ quote .Description }},
				Required:    {{ .Required }},
			},
			{{- end }}
		},
	}, adapter.sdkPromptHandler(requestContext))
	{{- end }}
	return nil
}
{{- else }}
func registerSDKPrompts(_ *mcpsdk.Server, _ *MCPAdapter, _ func(context.Context, *http.Request) context.Context) error {
	return nil
}
{{- end }}

func sdkToolAnnotations(raw any) (*mcpsdk.ToolAnnotations, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var annotations mcpsdk.ToolAnnotations
	if err := json.Unmarshal(data, &annotations); err != nil {
		return nil, err
	}
	return &annotations, nil
}

func sdkToolInputSchema(raw string) any {
	if raw == "" {
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage([]byte(raw))
}

func (a *MCPAdapter) sdkToolHandler(requestContext func(context.Context, *http.Request) context.Context) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		payload := &ToolsCallPayload{}
		if req != nil && req.Params != nil {
			payload.Name = req.Params.Name
			payload.Arguments = req.Params.Arguments
		}
		ctx = a.sdkRequestContext(ctx, req.GetSession(), req.GetExtra(), requestContext)
		stream := &sdkToolCallCollector{}
		if err := a.ToolsCall(ctx, payload, stream); err != nil {
			return nil, err
		}
		return sdkCallToolResult(stream.result())
	}
}

{{- if or .StaticPrompts .DynamicPrompts }}
func (a *MCPAdapter) sdkPromptHandler(requestContext func(context.Context, *http.Request) context.Context) mcpsdk.PromptHandler {
	return func(ctx context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		payload := &PromptsGetPayload{}
		if req != nil && req.Params != nil {
			payload.Name = req.Params.Name
			if req.Params.Arguments != nil {
				args, err := json.Marshal(req.Params.Arguments)
				if err != nil {
					return nil, err
				}
				payload.Arguments = args
			}
		}
		ctx = a.sdkRequestContext(ctx, req.GetSession(), req.GetExtra(), requestContext)
		result, err := a.PromptsGet(ctx, payload)
		if err != nil {
			return nil, err
		}
		return sdkGetPromptResult(result)
	}
}
{{- end }}

{{- if .Resources }}
func (a *MCPAdapter) sdkResourceHandler(requestContext func(context.Context, *http.Request) context.Context) mcpsdk.ResourceHandler {
	return func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		payload := &ResourcesReadPayload{}
		if req != nil && req.Params != nil {
			payload.URI = req.Params.URI
		}
		ctx = a.sdkRequestContext(ctx, req.GetSession(), req.GetExtra(), requestContext)
		result, err := a.ResourcesRead(ctx, payload)
		if err != nil {
			return nil, err
		}
		return sdkReadResourceResult(result)
	}
}
{{- end }}

func (a *MCPAdapter) sdkRequestContext(ctx context.Context, session mcpsdk.Session, extra *mcpsdk.RequestExtra, requestContext func(context.Context, *http.Request) context.Context) context.Context {
	if requestContext != nil {
		ctx = requestContext(ctx, sdkSyntheticHTTPRequest(extra))
	}
	if session == nil {
		a.markInitializedSession("")
		return ctx
	}
	sessionID := session.ID()
	if sessionID == "" {
		a.markInitializedSession("")
		return ctx
	}
	a.markInitializedSession(sessionID)
	return mcpruntime.WithSessionID(ctx, sessionID)
}

func sdkSyntheticHTTPRequest(extra *mcpsdk.RequestExtra) *http.Request {
	req := &http.Request{
		Method: http.MethodPost,
		Header: make(http.Header),
		URL:    &url.URL{Path: "/mcp"},
	}
	if extra == nil || extra.Header == nil {
		return req
	}
	req.Header = extra.Header.Clone()
	return req
}

type sdkToolCallCollector struct {
	parts     []*ToolsCallResult
	final     *ToolsCallResult
	streamErr error
}

func (c *sdkToolCallCollector) Send(_ context.Context, event ToolsCallEvent) error {
	res, ok := event.(*ToolsCallResult)
	if !ok {
		return fmt.Errorf("unexpected tools/call event type %T", event)
	}
	c.parts = append(c.parts, res)
	return nil
}

func (c *sdkToolCallCollector) SendAndClose(_ context.Context, event ToolsCallEvent) error {
	res, ok := event.(*ToolsCallResult)
	if !ok {
		return fmt.Errorf("unexpected tools/call final event type %T", event)
	}
	c.final = res
	return nil
}

func (c *sdkToolCallCollector) SendError(_ context.Context, _ string, err error) error {
	c.streamErr = err
	return nil
}

func (c *sdkToolCallCollector) result() *ToolsCallResult {
	if c == nil {
		return &ToolsCallResult{}
	}
	if c.streamErr != nil {
		item := &ContentItem{Type: "text", Text: stringPtr(c.streamErr.Error())}
		return &ToolsCallResult{
			Content: []*ContentItem{item},
			IsError: boolPtr(true),
		}
	}
	if len(c.parts) == 0 {
		if c.final == nil {
			return &ToolsCallResult{}
		}
		return c.final
	}
	merged := &ToolsCallResult{}
	for _, part := range c.parts {
		if part == nil {
			continue
		}
		merged.Content = append(merged.Content, part.Content...)
		if part.IsError != nil && *part.IsError {
			merged.IsError = boolPtr(true)
		}
	}
	if c.final != nil {
		merged.Content = append(merged.Content, c.final.Content...)
		if c.final.IsError != nil {
			merged.IsError = c.final.IsError
		}
	}
	return merged
}

func sdkCallToolResult(result *ToolsCallResult) (*mcpsdk.CallToolResult, error) {
	if result == nil {
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{}}, nil
	}
	content := make([]mcpsdk.Content, 0, len(result.Content))
	for _, item := range result.Content {
		converted, err := sdkContentFromItem(item)
		if err != nil {
			return nil, err
		}
		content = append(content, converted)
	}
	callResult := &mcpsdk.CallToolResult{Content: content}
	if result.IsError != nil {
		callResult.IsError = *result.IsError
	}
	return callResult, nil
}

{{- if or .StaticPrompts .DynamicPrompts }}
func sdkGetPromptResult(result *PromptsGetResult) (*mcpsdk.GetPromptResult, error) {
	if result == nil {
		return &mcpsdk.GetPromptResult{Messages: []*mcpsdk.PromptMessage{}}, nil
	}
	messages := make([]*mcpsdk.PromptMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		converted, err := sdkPromptMessage(message)
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted)
	}
	return &mcpsdk.GetPromptResult{
		Description: derefString(result.Description),
		Messages:    messages,
	}, nil
}
{{- end }}

{{- if .Resources }}
func sdkReadResourceResult(result *ResourcesReadResult) (*mcpsdk.ReadResourceResult, error) {
	if result == nil {
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{}}, nil
	}
	contents := make([]*mcpsdk.ResourceContents, 0, len(result.Contents))
	for _, content := range result.Contents {
		converted, err := sdkReadResourceContent(content)
		if err != nil {
			return nil, err
		}
		contents = append(contents, converted)
	}
	return &mcpsdk.ReadResourceResult{Contents: contents}, nil
}
{{- end }}

func sdkContentFromItem(item *ContentItem) (mcpsdk.Content, error) {
	if item == nil {
		return &mcpsdk.TextContent{}, nil
	}
	switch item.Type {
	case "text":
		return &mcpsdk.TextContent{Text: derefString(item.Text)}, nil
	case "image":
		data, err := sdkDecodeBase64(item.Data)
		if err != nil {
			return nil, err
		}
		return &mcpsdk.ImageContent{
			Data:     data,
			MIMEType: derefString(item.MimeType),
		}, nil
	case "audio":
		data, err := sdkDecodeBase64(item.Data)
		if err != nil {
			return nil, err
		}
		return &mcpsdk.AudioContent{
			Data:     data,
			MIMEType: derefString(item.MimeType),
		}, nil
	case "resource":
		resource, err := sdkResourceContents(item)
		if err != nil {
			return nil, err
		}
		return &mcpsdk.EmbeddedResource{Resource: resource}, nil
	default:
		if item.URI != nil {
			resource, err := sdkResourceContents(item)
			if err != nil {
				return nil, err
			}
			return &mcpsdk.EmbeddedResource{Resource: resource}, nil
		}
		return nil, fmt.Errorf("unsupported MCP content type %q", item.Type)
	}
}

{{- if or .StaticPrompts .DynamicPrompts }}
func sdkPromptMessage(message *PromptMessage) (*mcpsdk.PromptMessage, error) {
	if message == nil {
		return &mcpsdk.PromptMessage{Content: &mcpsdk.TextContent{}}, nil
	}
	content, err := sdkContentFromMessageContent(message.Content)
	if err != nil {
		return nil, err
	}
	return &mcpsdk.PromptMessage{
		Role:    mcpsdk.Role(message.Role),
		Content: content,
	}, nil
}

func sdkContentFromMessageContent(item *MessageContent) (mcpsdk.Content, error) {
	if item == nil {
		return &mcpsdk.TextContent{}, nil
	}
	contentItem := &ContentItem{
		Type:     item.Type,
		Text:     item.Text,
		Data:     item.Data,
		MimeType: item.MimeType,
		URI:      item.URI,
	}
	return sdkContentFromItem(contentItem)
}
{{- end }}

func sdkResourceContents(item *ContentItem) (*mcpsdk.ResourceContents, error) {
	resource := &mcpsdk.ResourceContents{
		URI:      derefString(item.URI),
		MIMEType: derefString(item.MimeType),
		Text:     derefString(item.Text),
	}
	if item.Data != nil {
		data, err := sdkDecodeBase64(item.Data)
		if err != nil {
			return nil, err
		}
		resource.Blob = data
	}
	return resource, nil
}

{{- if .Resources }}
func sdkReadResourceContent(item *ResourceContent) (*mcpsdk.ResourceContents, error) {
	if item == nil {
		return &mcpsdk.ResourceContents{}, nil
	}
	resource := &mcpsdk.ResourceContents{
		URI:      item.URI,
		MIMEType: derefString(item.MimeType),
		Text:     derefString(item.Text),
	}
	if item.Blob != nil {
		data, err := sdkDecodeBase64(item.Blob)
		if err != nil {
			return nil, err
		}
		resource.Blob = data
	}
	return resource, nil
}
{{- end }}

func sdkDecodeBase64(raw *string) ([]byte, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(*raw)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolPtr(v bool) *bool {
	return &v
}
