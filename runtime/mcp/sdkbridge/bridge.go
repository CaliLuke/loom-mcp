// Package sdkbridge owns service-independent wiring for official SDK-backed MCP servers.
package sdkbridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	loomhttp "github.com/CaliLuke/loom/http"
	"github.com/CaliLuke/loom/observability/transport"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CompatibilityVersion is the generated descriptor contract supported by this runtime.
const CompatibilityVersion = 1

// Config describes one generated MCP service without erasing its typed handlers.
type Config struct {
	CompatibilityVersion int
	Implementation       mcpsdk.Implementation
	Tools                func() ([]ToolBinding, error)
	Resources            func() ([]ResourceBinding, error)
	Prompts              func() ([]PromptBinding, error)
	CompletionHandler    func(context.Context, *mcpsdk.CompleteRequest) (*mcpsdk.CompleteResult, error)
	WatchableResource    func(string) bool
	Sessions             SessionHooks
	Options              Options
}

// Options configures common SDK server and HTTP behavior.
type Options struct {
	RequestContext    func(context.Context, *http.Request) context.Context
	TransportObserver transport.Observer
	RuntimeCORS       *loomhttp.RuntimeCORSPolicy
	Server            *mcpsdk.ServerOptions
	StreamableHTTP    *mcpsdk.StreamableHTTPOptions
	OriginProtection  *http.CrossOriginProtection
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

// SessionHooks keeps generated session state and application-owned principals outside the bridge.
type SessionHooks struct {
	AssertPrincipal    func(context.Context, string) error
	MarkInitialized    func(string)
	CapturePrincipal   func(context.Context, string)
	Clear              func(string)
	IsInvalidSessionID func(error) bool
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
}

// NewServer validates generated/runtime compatibility and installs common SDK behavior.
func NewServer(config Config) (*Server, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	server, err := newSDKServer(config)
	if err != nil {
		return nil, err
	}
	handler := serverHTTPHandler(server, config.Options, config.Sessions)
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
	server.AddReceivingMiddleware(jsonRPCErrorMiddleware)
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

func serverHTTPHandler(server *mcpsdk.Server, options Options, sessions SessionHooks) http.Handler {
	configuredStreamableOptions := streamableHTTPOptions(options.StreamableHTTP)
	originProtection := options.OriginProtection
	if originProtection == nil {
		originProtection = http.NewCrossOriginProtection()
	}
	handler := newHandler(server, options.RequestContext, configuredStreamableOptions, sessions)
	if options.TransportObserver != nil {
		handler = transport.HTTPMiddleware(options.TransportObserver)(handler)
	}
	if options.RuntimeCORS != nil {
		handler = runtimeCORSHandler(handler, *options.RuntimeCORS)
	}
	return originValidationHandler(handler, originProtection)
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

func newHandler(server *mcpsdk.Server, requestContext func(context.Context, *http.Request) context.Context, configuredStreamableOptions *mcpsdk.StreamableHTTPOptions, sessions SessionHooks) http.Handler {
	sdkStreamableOptions := *configuredStreamableOptions
	base := mcpruntime.StreamableHTTPNegotiation(
		mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
			return server
		}, &sdkStreamableOptions),
		sdkStreamableOptions.JSONResponse,
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(mcpruntime.WithRequestHeaders(r.Context(), r.Header))
		if requestContext != nil {
			r = r.WithContext(requestContext(r.Context(), r))
		}
		if sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" && sessions.AssertPrincipal != nil {
			if err := sessions.AssertPrincipal(r.Context(), sessionID); err != nil {
				writeSessionError(w, err, sessions.IsInvalidSessionID)
				return
			}
		}
		transportObservation, transportWriter := transport.BeginHTTPRequest(r.Context(), w, "mcp", r.Method, r)
		defer transportObservation.End()
		observer := &responseObserver{
			ResponseWriter: transportWriter,
			onSessionIssued: func(sessionID string) {
				if sessions.MarkInitialized != nil {
					sessions.MarkInitialized(sessionID)
				}
				if sessions.CapturePrincipal != nil {
					sessions.CapturePrincipal(r.Context(), sessionID)
				}
			},
		}
		base.ServeHTTP(observer, r)
		if observer.statusCode < http.StatusBadRequest {
			observer.captureSession()
		}
		if r.Method == http.MethodDelete && observer.statusCode < http.StatusBadRequest && sessions.Clear != nil {
			sessions.Clear(r.Header.Get(mcpruntime.HeaderKeySessionID))
		}
		if observer.statusCode >= http.StatusBadRequest {
			transportObservation.Fail(transport.ReasonHandlerError)
		}
	})
}

func streamableHTTPOptions(opts *mcpsdk.StreamableHTTPOptions) *mcpsdk.StreamableHTTPOptions {
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

func originValidationHandler(next http.Handler, protection *http.CrossOriginProtection) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := validateOrigin(protection, r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validateOrigin(protection *http.CrossOriginProtection, r *http.Request) error {
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return protection.Check(r)
	}
	if len(origins) != 1 || origins[0] == "" {
		return errors.New("invalid Origin header")
	}
	parsedOrigin, err := url.Parse(origins[0])
	if err != nil || parsedOrigin.Scheme == "" || parsedOrigin.Host == "" || origins[0] != parsedOrigin.Scheme+"://"+parsedOrigin.Host {
		return errors.New("invalid Origin header")
	}

	originRequest := *r
	switch originRequest.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		originRequest.Method = "MCP-ORIGIN-CHECK"
	}
	if r.Header.Get("Sec-Fetch-Site") != "" {
		originRequest.Header = r.Header.Clone()
		originRequest.Header.Del("Sec-Fetch-Site")
	}
	return protection.Check(&originRequest)
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

func writeSessionError(w http.ResponseWriter, err error, isInvalid func(error) bool) {
	if isInvalid != nil && isInvalid(err) {
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
