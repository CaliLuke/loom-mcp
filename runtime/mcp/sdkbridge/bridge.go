// Package sdkbridge owns service-independent wiring for official SDK-backed MCP servers.
package sdkbridge

import (
	"context"
	"errors"
	"fmt"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	loomhttp "github.com/CaliLuke/loom/http"
	"github.com/CaliLuke/loom/observability/transport"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CompatibilityVersion is the generated descriptor contract supported by this runtime.
const CompatibilityVersion = 3

// Config describes one generated MCP service without erasing its typed handlers.
type Config struct {
	CompatibilityVersion int
	Implementation       mcpsdk.Implementation
	Tools                func() ([]ToolBinding, error)
	Resources            func() ([]ResourceBinding, error)
	Prompts              func() ([]PromptBinding, error)
	CompletionHandler    func(context.Context, *mcpsdk.CompleteRequest) (*mcpsdk.CompleteResult, error)
	WatchableResource    func(string) bool
	Sessions             *SessionState
	Options              Options
}

// StreamableHTTPOptions exposes the supported official SDK transport settings.
// Origin validation is configured separately through OriginProtection.
type StreamableHTTPOptions struct {
	// Stateless disables sessions, GET, and DELETE. Server-to-client requests are unavailable.
	Stateless bool
	// JSONResponse makes HTTP POST return application/json instead of an SSE stream.
	JSONResponse bool
	// Logger receives SDK transport logs. A nil logger disables these logs.
	Logger *slog.Logger
	// EventStore enables clients to resume interrupted streams.
	EventStore mcpsdk.EventStore
	// SessionTimeout closes idle sessions after this duration. Zero keeps idle sessions open.
	SessionTimeout time.Duration
	// DisableLocalhostProtection disables the SDK's DNS-rebinding protection for localhost servers.
	DisableLocalhostProtection bool
	// MaxRequestBodyBytes limits request bodies. Zero uses the SDK default. A negative value disables the limit and is unsafe.
	MaxRequestBodyBytes int64
	// PropagateRequestCancellation ties handler contexts to their HTTP request for protocol version 2026-07-28 and later.
	PropagateRequestCancellation bool
}

// OriginProtection configures origin validation for every MCP HTTP method.
type OriginProtection struct {
	TrustedOrigins []string
	DenyHandler    http.Handler
}

// Options configures common SDK server and HTTP behavior.
type Options struct {
	RequestContext    func(context.Context, *http.Request) context.Context
	TransportObserver transport.Observer
	RuntimeCORS       *loomhttp.RuntimeCORSPolicy
	Server            *mcpsdk.ServerOptions
	StreamableHTTP    *StreamableHTTPOptions
	OriginProtection  *OriginProtection
}

// ToolBinding pairs a generated SDK descriptor with its typed handler.
type ToolBinding struct {
	Tool    *mcpsdk.Tool
	Handler mcpsdk.ToolHandler
}

// ResourceBinding pairs a generated SDK descriptor with its typed handler.
// Exactly one of Resource or Template must be set.
type ResourceBinding struct {
	Resource *mcpsdk.Resource
	Template *mcpsdk.ResourceTemplate
	Handler  mcpsdk.ResourceHandler
}

// PromptBinding pairs a generated SDK descriptor with its typed handler.
type PromptBinding struct {
	Prompt  *mcpsdk.Prompt
	Handler mcpsdk.PromptHandler
}

// Server is a configured official SDK server and its HTTP handler.
type Server struct {
	Handler http.Handler
	SDK     *mcpsdk.Server

	watchableResource func(string) bool
}

type responseObserver struct {
	http.ResponseWriter
	statusCode      int
	onSessionIssued func(string)
	sessionOnce     sync.Once
	originRejected  bool
}

// NewServer validates generated/runtime compatibility and installs common SDK behavior.
func NewServer(config Config) (*Server, error) {
	if config.Sessions == nil {
		config.Sessions = NewSessionState(nil)
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	server, err := newSDKServer(config)
	if err != nil {
		return nil, err
	}
	handler, err := serverHTTPHandler(server, config.Options, config.Sessions)
	if err != nil {
		return nil, err
	}
	return &Server{Handler: handler, SDK: server, watchableResource: config.WatchableResource}, nil
}

func validateConfig(config Config) error {
	if config.CompatibilityVersion != CompatibilityVersion {
		return fmt.Errorf("MCP SDK bridge compatibility mismatch: generated version %d, runtime version %d", config.CompatibilityVersion, CompatibilityVersion)
	}
	if config.WatchableResource != nil && config.Options.StreamableHTTP != nil && config.Options.StreamableHTTP.Stateless {
		return fmt.Errorf("watchable MCP resources require stateful Streamable HTTP sessions")
	}
	return nil
}

func newSDKServer(config Config) (*mcpsdk.Server, error) {
	serverOptions := serverOptions(config.Options.Server, config.CompletionHandler, config.WatchableResource)
	server := mcpsdk.NewServer(&config.Implementation, serverOptions)
	server.AddReceivingMiddleware(
		jsonRPCErrorMiddleware,
		requestContextMiddleware(config.Options.RequestContext, config.Sessions),
	)
	if err := loadToolBindings(server, config.Tools); err != nil {
		return nil, err
	}
	if err := loadResourceBindings(server, config.Resources); err != nil {
		return nil, err
	}
	if err := loadPromptBindings(server, config.Prompts); err != nil {
		return nil, err
	}
	return server, nil
}

func loadToolBindings(server *mcpsdk.Server, loader func() ([]ToolBinding, error)) error {
	bindings, err := loadBindings(loader)
	if err != nil {
		return fmt.Errorf("load MCP SDK tool bindings: %w", err)
	}
	return addToolBindings(server, bindings)
}

func loadResourceBindings(server *mcpsdk.Server, loader func() ([]ResourceBinding, error)) error {
	bindings, err := loadBindings(loader)
	if err != nil {
		return fmt.Errorf("load MCP SDK resource bindings: %w", err)
	}
	return addResourceBindings(server, bindings)
}

func loadPromptBindings(server *mcpsdk.Server, loader func() ([]PromptBinding, error)) error {
	bindings, err := loadBindings(loader)
	if err != nil {
		return fmt.Errorf("load MCP SDK prompt bindings: %w", err)
	}
	return addPromptBindings(server, bindings)
}

func serverHTTPHandler(server *mcpsdk.Server, options Options, sessions *SessionState) (http.Handler, error) {
	handler := newHandler(server, options.RequestContext, streamableHTTPOptions(options.StreamableHTTP), sessions)
	if options.RuntimeCORS != nil {
		handler = runtimeCORSHandler(handler, *options.RuntimeCORS)
	}
	var err error
	handler, err = originValidationHandler(handler, options.OriginProtection)
	if err != nil {
		return nil, err
	}
	handler = observeHTTPHandler(handler)
	if options.TransportObserver != nil {
		handler = transport.HTTPMiddleware(options.TransportObserver)(handler)
	}
	return handler, nil
}

func addToolBindings(server *mcpsdk.Server, bindings []ToolBinding) error {
	for _, binding := range bindings {
		if binding.Tool == nil || binding.Handler == nil {
			return fmt.Errorf("MCP SDK bridge tool binding requires a descriptor and handler")
		}
		server.AddTool(binding.Tool, binding.Handler)
	}
	return nil
}

func addResourceBindings(server *mcpsdk.Server, bindings []ResourceBinding) error {
	for _, binding := range bindings {
		if binding.Handler == nil || (binding.Resource == nil) == (binding.Template == nil) {
			return fmt.Errorf("MCP SDK bridge resource binding requires exactly one descriptor and a handler")
		}
		if binding.Template != nil {
			server.AddResourceTemplate(binding.Template, binding.Handler)
			continue
		}
		server.AddResource(binding.Resource, binding.Handler)
	}
	return nil
}

func addPromptBindings(server *mcpsdk.Server, bindings []PromptBinding) error {
	for _, binding := range bindings {
		if binding.Prompt == nil || binding.Handler == nil {
			return fmt.Errorf("MCP SDK bridge prompt binding requires a descriptor and handler")
		}
		server.AddPrompt(binding.Prompt, binding.Handler)
	}
	return nil
}

// ResourceUpdated notifies subscribers that a generated watchable resource changed.
func (s *Server) ResourceUpdated(ctx context.Context, uri string) error {
	if s == nil || s.SDK == nil {
		return fmt.Errorf("MCP SDK server is not initialized")
	}
	if s.watchableResource == nil || !s.watchableResource(uri) {
		return fmt.Errorf("unknown watchable MCP resource %q", uri)
	}
	return s.SDK.ResourceUpdated(ctx, &mcpsdk.ResourceUpdatedNotificationParams{URI: uri})
}

func serverOptions(opts *mcpsdk.ServerOptions, completion func(context.Context, *mcpsdk.CompleteRequest) (*mcpsdk.CompleteResult, error), watchable func(string) bool) *mcpsdk.ServerOptions {
	if opts == nil {
		opts = &mcpsdk.ServerOptions{}
	} else {
		copied := *opts
		opts = &copied
	}
	if opts.Capabilities == nil {
		opts.Capabilities = &mcpsdk.ServerCapabilities{}
	}
	if opts.CompletionHandler == nil {
		opts.CompletionHandler = completion
	}
	if watchable != nil {
		subscribe := opts.SubscribeHandler
		unsubscribe := opts.UnsubscribeHandler
		opts.SubscribeHandler = func(ctx context.Context, req *mcpsdk.SubscribeRequest) error {
			uri := subscriptionURI(req)
			if !watchable(uri) {
				return fmt.Errorf("unknown watchable MCP resource %q", uri)
			}
			if subscribe != nil {
				return subscribe(ctx, req)
			}
			return nil
		}
		opts.UnsubscribeHandler = func(ctx context.Context, req *mcpsdk.UnsubscribeRequest) error {
			uri := unsubscriptionURI(req)
			if !watchable(uri) {
				return fmt.Errorf("unknown watchable MCP resource %q", uri)
			}
			if unsubscribe != nil {
				return unsubscribe(ctx, req)
			}
			return nil
		}
	}
	return opts
}
func newHandler(server *mcpsdk.Server, requestContext func(context.Context, *http.Request) context.Context, configuredStreamableOptions *mcpsdk.StreamableHTTPOptions, sessions *SessionState) http.Handler {
	sdkStreamableOptions := *configuredStreamableOptions
	base := mcpruntime.StreamableHTTPNegotiation(
		mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
			return server
		}, &sdkStreamableOptions),
		sdkStreamableOptions.JSONResponse,
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(mcpruntime.WithRequestHeaders(r.Context(), r.Header))
		if r.Method != http.MethodPost && requestContext != nil {
			r = r.WithContext(requestContext(r.Context(), r))
		}
		if sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
			if err := sessions.AssertPrincipal(r.Context(), sessionID); err != nil {
				writeSessionError(w, err)
				return
			}
		}
		observer := &responseObserver{
			ResponseWriter:  w,
			onSessionIssued: sessions.MarkInitialized,
		}
		base.ServeHTTP(observer, sdkTransportRequest(r))
		if observer.statusCode < http.StatusBadRequest {
			observer.captureSession()
		}
		if r.Method == http.MethodDelete && observer.statusCode < http.StatusBadRequest {
			sessions.Clear(r.Header.Get(mcpruntime.HeaderKeySessionID))
		}
	})
}
func sdkTransportRequest(r *http.Request) *http.Request {
	if r == nil || (len(r.Header.Values("Origin")) == 0 && r.Header.Get("Sec-Fetch-Site") == "") {
		return r
	}
	cloned := *r
	cloned.Header = r.Header.Clone()
	cloned.Header.Del("Origin")
	cloned.Header.Del("Sec-Fetch-Site")
	return &cloned
}
func observeHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observation, observedWriter := transport.BeginHTTPRequest(r.Context(), w, "mcp", r.Method, r)
		defer observation.End()
		response := &responseObserver{ResponseWriter: observedWriter}
		next.ServeHTTP(response, r)
		if response.originRejected || response.statusCode >= http.StatusBadRequest {
			observation.Fail(transport.ReasonHandlerError)
		}
	})
}

func streamableHTTPOptions(opts *StreamableHTTPOptions) *mcpsdk.StreamableHTTPOptions {
	configured := &mcpsdk.StreamableHTTPOptions{MaxRequestBodyBytes: mcpsdk.DefaultMaxRequestBodyBytes}
	if opts == nil {
		return configured
	}
	configured.Stateless = opts.Stateless
	configured.JSONResponse = opts.JSONResponse
	configured.Logger = opts.Logger
	configured.EventStore = opts.EventStore
	configured.SessionTimeout = opts.SessionTimeout
	configured.DisableLocalhostProtection = opts.DisableLocalhostProtection
	configured.PropagateRequestCancellation = opts.PropagateRequestCancellation
	if opts.MaxRequestBodyBytes != 0 {
		configured.MaxRequestBodyBytes = opts.MaxRequestBodyBytes
	}
	return configured
}

func originValidationHandler(next http.Handler, options *OriginProtection) (http.Handler, error) {
	protection := http.NewCrossOriginProtection()
	var denyHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
	})
	if options != nil {
		for _, origin := range options.TrustedOrigins {
			if err := protection.AddTrustedOrigin(origin); err != nil {
				return nil, fmt.Errorf("add trusted MCP origin %q: %w", origin, err)
			}
		}
		if options.DenyHandler != nil {
			denyHandler = options.DenyHandler
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest, err := originCheckRequest(r)
		if err != nil || protection.Check(checkRequest) != nil {
			if marker, ok := w.(interface{ markOriginRejected() }); ok {
				marker.markOriginRejected()
			}
			denyHandler.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func originCheckRequest(r *http.Request) (*http.Request, error) {
	if r == nil {
		return r, nil
	}
	checkRequest := *r
	origins := r.Header.Values("Origin")
	if len(origins) > 0 {
		if len(origins) != 1 || origins[0] == "" {
			return nil, errors.New("invalid Origin header")
		}
		parsedOrigin, err := url.Parse(origins[0])
		if err != nil || parsedOrigin.Scheme == "" || parsedOrigin.Host == "" || origins[0] != parsedOrigin.Scheme+"://"+parsedOrigin.Host {
			return nil, errors.New("invalid Origin header")
		}
		fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
		if !strings.EqualFold(parsedOrigin.Host, r.Host) && (fetchSite == "same-origin" || fetchSite == "none") {
			checkRequest.Header = r.Header.Clone()
			checkRequest.Header.Del("Sec-Fetch-Site")
		}
	}
	switch checkRequest.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		checkRequest.Method = "MCP-ORIGIN-CHECK"
	}
	return &checkRequest, nil
}

func runtimeCORSHandler(next http.Handler, policy loomhttp.RuntimeCORSPolicy) http.Handler {
	actual := policy.Handler(next.ServeHTTP)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			policy.HandlePreflight(w, r, []string{http.MethodDelete, http.MethodGet, http.MethodPost})
			return
		}
		actual(w, r)
	})
}

func jsonRPCErrorMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil {
			return nil, mcpruntime.NormalizeJSONRPCError(method, err)
		}
		return result, nil
	}
}
func requestContextMiddleware(requestContext func(context.Context, *http.Request) context.Context, sessions *SessionState) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			var headers http.Header
			if req != nil && req.GetExtra() != nil {
				headers = req.GetExtra().Header
			}
			ctx = mcpruntime.WithRequestHeaders(ctx, headers)
			if requestContext != nil {
				httpRequest := (&http.Request{Method: http.MethodPost, Header: headers.Clone(), URL: &url.URL{}}).WithContext(ctx)
				ctx = requestContext(ctx, httpRequest)
			}
			sessionID := ""
			if req != nil {
				if session, ok := req.GetSession().(*mcpsdk.ServerSession); ok && session != nil {
					sessionID = session.ID()
				}
			}
			if method != "initialize" && sessionID != "" {
				if err := sessions.AssertPrincipal(ctx, sessionID); err != nil {
					return nil, err
				}
			}
			result, err := next(ctx, method, req)
			if err == nil && method == "initialize" && sessionID != "" {
				sessions.MarkInitialized(sessionID)
				sessions.CapturePrincipal(ctx, sessionID)
			}
			return result, err
		}
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	if IsInvalidSessionID(err) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusForbidden)
}

func subscriptionURI(req *mcpsdk.SubscribeRequest) string {
	if req == nil || req.Params == nil {
		return ""
	}
	return req.Params.URI
}

func unsubscriptionURI(req *mcpsdk.UnsubscribeRequest) string {
	if req == nil || req.Params == nil {
		return ""
	}
	return req.Params.URI
}

func (w *responseObserver) captureSession() {
	if w == nil || w.onSessionIssued == nil {
		return
	}
	sessionID := w.Header().Get(mcpruntime.HeaderKeySessionID)
	if sessionID == "" {
		return
	}
	w.sessionOnce.Do(func() {
		w.onSessionIssued(sessionID)
	})
}

func (w *responseObserver) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseObserver) markOriginRejected() {
	w.originRejected = true
}

func (w *responseObserver) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	if statusCode < http.StatusBadRequest {
		w.captureSession()
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseObserver) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	if w.statusCode < http.StatusBadRequest {
		w.captureSession()
	}
	return w.ResponseWriter.Write(data)
}

func loadBindings[T any](provider func() ([]T, error)) ([]T, error) {
	if provider == nil {
		return nil, nil
	}
	return provider()
}
