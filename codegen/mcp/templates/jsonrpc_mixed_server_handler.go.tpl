// ServeHTTP handles JSON-RPC requests with content negotiation for mixed HTTP/SSE transports.
func (s *{{ .ServerStruct }}) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleStreamableHTTPGet(w, r)
	case http.MethodDelete:
		s.handleStreamableHTTPDelete(w, r)
	case http.MethodPost:
		if s.shouldHandleSSE(r) {
			s.handleSSE(w, r)
			return
		}

		// Otherwise handle as regular JSON-RPC HTTP request
		s.handleHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *{{ .ServerStruct }}) shouldHandleSSE(r *http.Request) bool {
	if r == nil || r.Body == nil {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.errhandler(r.Context(), &jsonrpcResponseCapture{}, fmt.Errorf("failed to inspect JSON-RPC request for SSE dispatch: %w", err))
		return false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] == '[' {
		return false
	}

	var req jsonrpc.RawRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		s.errhandler(r.Context(), &jsonrpcResponseCapture{}, fmt.Errorf("failed to inspect JSON-RPC request for SSE dispatch: %w", err))
		return false
	}

	switch req.Method {
{{- range .Endpoints }}
	{{- if .Method.ServerStream }}
	case "{{ .Method.Name }}":
		return true
	{{- end }}
{{- end }}
	default:
		return false
	}
}

func (s *{{ .ServerStruct }}) handleStreamableHTTPGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID)
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	unregister, err := s.streamableHTTPSessions.RegisterListener(sessionID, cancel)
	if err != nil {
		cancel()
		s.writeStreamableHTTPSessionError(w, err)
		return
	}
	defer unregister()
	req := &jsonrpc.RawRequest{
		JSONRPC: "2.0",
		ID:      "events-stream",
		Method:  "events/stream",
	}
	if err := s.EventsStream(ctx, r.WithContext(ctx), req, w); err != nil {
		s.errhandler(ctx, w, fmt.Errorf("handler error for %s: %w", "events/stream", err))
	}
}

func (s *{{ .ServerStruct }}) handleStreamableHTTPDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID)
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}
	if err := s.streamableHTTPSessions.Terminate(sessionID); err != nil {
		s.writeStreamableHTTPSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
