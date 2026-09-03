// Package sdkbridge owns service-independent wiring for official SDK-backed MCP servers.
package sdkbridge

import (
	"context"
	"fmt"
	"net/http"
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
}

// ToolBinding pairs a generated SDK descriptor with its typed handler.
type ToolBinding struct {
	Tool    *mcpsdk.Tool
	Handler mcpsdk.ToolHandler
}

// ResourceBinding pairs a generated SDK descriptor with its typed handler.
type ResourceBinding struct {
	Resource *mcpsdk.Resource
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
	if config.CompatibilityVersion != CompatibilityVersion {
		return nil, fmt.Errorf("MCP SDK bridge compatibility mismatch: generated version %d, runtime version %d", config.CompatibilityVersion, CompatibilityVersion)
	}
	if config.WatchableResource != nil && config.Options.StreamableHTTP != nil && config.Options.StreamableHTTP.Stateless {
		return nil, fmt.Errorf("watchable MCP resources require stateful Streamable HTTP sessions")
	}
	tools, err := loadBindings(config.Tools)
	if err != nil {
		return nil, fmt.Errorf("load MCP SDK tool bindings: %w", err)
	}
	resources, err := loadBindings(config.Resources)
	if err != nil {
		return nil, fmt.Errorf("load MCP SDK resource bindings: %w", err)
	}
	prompts, err := loadBindings(config.Prompts)
	if err != nil {
		return nil, fmt.Errorf("load MCP SDK prompt bindings: %w", err)
	}
	serverOptions := serverOptions(config.Options.Server, config.CompletionHandler, config.WatchableResource)
	server := mcpsdk.NewServer(&config.Implementation, serverOptions)
	server.AddReceivingMiddleware(jsonRPCErrorMiddleware)
	for _, binding := range tools {
		if binding.Tool == nil || binding.Handler == nil {
			return nil, fmt.Errorf("MCP SDK bridge tool binding requires a descriptor and handler")
		}
		server.AddTool(binding.Tool, binding.Handler)
	}
	for _, binding := range resources {
		if binding.Resource == nil || binding.Handler == nil {
			return nil, fmt.Errorf("MCP SDK bridge resource binding requires a descriptor and handler")
		}
		server.AddResource(binding.Resource, binding.Handler)
	}
	for _, binding := range prompts {
		if binding.Prompt == nil || binding.Handler == nil {
			return nil, fmt.Errorf("MCP SDK bridge prompt binding requires a descriptor and handler")
		}
		server.AddPrompt(binding.Prompt, binding.Handler)
	}

	handler := newHandler(server, config.Options.RequestContext, config.Options.StreamableHTTP, config.Sessions)
	if config.Options.TransportObserver != nil {
		handler = transport.HTTPMiddleware(config.Options.TransportObserver)(handler)
	}
	if config.Options.RuntimeCORS != nil {
		handler = runtimeCORSHandler(handler, *config.Options.RuntimeCORS)
	}
	return &Server{Handler: handler, SDK: server, watchableResource: config.WatchableResource}, nil
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
		opts.Capabilities = &mcpsdk.ServerCapabilities{Logging: &mcpsdk.LoggingCapabilities{}}
	} else {
		capabilities := *opts.Capabilities
		opts.Capabilities = &capabilities
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

func newHandler(server *mcpsdk.Server, requestContext func(context.Context, *http.Request) context.Context, streamableOptions *mcpsdk.StreamableHTTPOptions, sessions SessionHooks) http.Handler {
	configuredStreamableOptions := streamableHTTPOptions(streamableOptions)
	crossOriginProtection := configuredStreamableOptions.CrossOriginProtection
	configuredStreamableOptions.CrossOriginProtection = nil
	base := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, configuredStreamableOptions)
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
		if err := crossOriginProtection.Check(r); err != nil {
			http.Error(transportWriter, err.Error(), http.StatusForbidden)
			transportObservation.Fail(transport.ReasonHandlerError)
			return
		}
		if requestAllowsBodyInspection(r, configuredStreamableOptions) && rejectNullRequestID(transportWriter, r, configuredStreamableOptions.MaxRequestBodyBytes) {
			transportObservation.Fail(transport.ReasonHandlerError)
			return
		}
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
	if opts == nil {
		return &mcpsdk.StreamableHTTPOptions{
			CrossOriginProtection: http.NewCrossOriginProtection(),
			MaxRequestBodyBytes:   mcpsdk.DefaultMaxRequestBodyBytes,
		}
	}
	configured := *opts
	if configured.CrossOriginProtection == nil {
		configured.CrossOriginProtection = http.NewCrossOriginProtection()
	}
	if configured.MaxRequestBodyBytes == 0 {
		configured.MaxRequestBodyBytes = mcpsdk.DefaultMaxRequestBodyBytes
	}
	return &configured
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
