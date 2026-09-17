package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Transport selects the Codex streaming transport.
type Transport uint8

const (
	// TransportAuto prefers WebSocket and safely falls back to SSE before output.
	TransportAuto Transport = iota
	// TransportSSE uses streamable HTTP server-sent events.
	TransportSSE
	// TransportWebSocket uses one fresh WebSocket per request.
	TransportWebSocket
)

// Options configures the Codex subscription provider.
type Options struct {
	CredentialSource  CredentialSource
	HTTPClient        *http.Client
	WebSocketDialer   *websocket.Dialer
	Transport         Transport
	ClientVersion     string
	ResponsesLite     bool
	StreamIdleTimeout time.Duration
	DefaultModel      string
	HighModel         string
	SmallModel        string
}

// Client implements model.Provider through the private ChatGPT Codex Responses
// wire contract. It intentionally does not implement model.TokenCounter.
type Client struct {
	credentials CredentialSource
	httpClient  *http.Client
	wsDialer    *websocket.Dialer
	transport   Transport
	version     string
	lite        bool
	idleTimeout time.Duration
	model       string
	highModel   string
	smallModel  string
}

// New constructs a Codex subscription provider with injected credentials.
func New(options Options) (*Client, error) { //nolint:maintidx // Constructor validation and defensive option copies stay together.
	if options.CredentialSource == nil {
		return nil, errors.New("codex: credential source is required")
	}
	if source, ok := options.CredentialSource.(CredentialSourceFunc); ok && source == nil {
		return nil, errors.New("codex: credential source is required")
	}
	if strings.TrimSpace(options.DefaultModel) == "" {
		return nil, errors.New("codex: default model is required")
	}
	if options.Transport > TransportWebSocket {
		return nil, fmt.Errorf("codex: invalid transport %d", options.Transport)
	}
	if options.StreamIdleTimeout < 0 {
		return nil, errors.New("codex: stream idle timeout must not be negative")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	httpClient := *options.HTTPClient
	httpClient.Jar = nil
	baseTransport := httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	if transport, ok := baseTransport.(*http.Transport); ok {
		transport = transport.Clone()
		transport.DisableCompression = true
		httpClient.Transport = transport
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if options.WebSocketDialer == nil {
		options.WebSocketDialer = websocket.DefaultDialer
	}
	webSocketDialer := *options.WebSocketDialer
	webSocketDialer.Jar = nil
	webSocketDialer.Subprotocols = nil
	webSocketDialer.EnableCompression = false
	version := strings.TrimSpace(options.ClientVersion)
	if version == "" {
		version = defaultClientVersion
	}
	if !safeProviderValue.MatchString(version) {
		return nil, errors.New("codex: client version must be a safe header value of at most 128 bytes")
	}
	if options.StreamIdleTimeout == 0 {
		options.StreamIdleTimeout = defaultIdleTimeout
	}
	return &Client{
		credentials: options.CredentialSource,
		httpClient:  &httpClient,
		wsDialer:    &webSocketDialer,
		transport:   options.Transport,
		version:     version,
		lite:        options.ResponsesLite,
		idleTimeout: options.StreamIdleTimeout,
		model:       strings.TrimSpace(options.DefaultModel),
		highModel:   strings.TrimSpace(options.HighModel),
		smallModel:  strings.TrimSpace(options.SmallModel),
	}, nil
}

// Complete consumes a Codex stream to literal EOF and returns its terminal response.
func (c *Client) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	stream, err := c.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	var receiveErr error
	for receiveErr == nil {
		_, receiveErr = stream.Recv()
	}
	if errors.Is(receiveErr, io.EOF) {
		receiveErr = nil
	}
	response := stream.Response()
	closeErr := stream.Close()
	if err := errors.Join(receiveErr, closeErr); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("codex: stream ended without a terminal response")
	}
	return response, nil
}

// Stream validates and starts a raw Codex stream.
func (c *Client) Stream(ctx context.Context, request *model.Request) (model.Streamer, error) {
	built, err := c.buildRequest(request)
	if err != nil {
		return nil, err
	}
	if err := built.prepare(c.transport); err != nil {
		return nil, err
	}
	credentials, err := resolveCredentials(ctx, c.credentials)
	if err != nil {
		return nil, err
	}
	return c.startStream(ctx, built, credentials)
}

func (c *Client) resolveModelID(request *model.Request) string {
	if request.Model != "" {
		return request.Model
	}
	switch request.ModelClass {
	case model.ModelClassDefault:
	case model.ModelClassHighReasoning:
		if c.highModel != "" {
			return c.highModel
		}
	case model.ModelClassSmall:
		if c.smallModel != "" {
			return c.smallModel
		}
	}
	return c.model
}
