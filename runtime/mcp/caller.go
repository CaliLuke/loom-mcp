package mcp

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Caller invokes MCP tools on behalf of the runtime-generated adapters. It is
// implemented by transport-specific clients (stdio, HTTP streaming, etc.).
type Caller interface {
	CallTool(ctx context.Context, req CallRequest) (CallResponse, error)
}

// CallRequest describes the toolset/tool invocation issued by the runtime.
type CallRequest struct {
	// Suite identifies the MCP toolset (server name) associated with the tool.
	Suite string
	// Tool is the MCP-local tool identifier (without the suite prefix).
	Tool string
	// Payload is the JSON-encoded tool arguments produced by the runtime.
	Payload jsontext.Value
}

// CallResponse captures the MCP tool result returned by the caller.
type CallResponse struct {
	// Result is the JSON payload returned by the MCP server.
	Result jsontext.Value
	// Structured carries the full structured MCP content payload, including text
	// items that may also contribute to Result.
	Structured jsontext.Value
}

// ToolCallError reports a remote MCP tool failure that was returned as an
// isError tool result rather than a transport/protocol error.
type ToolCallError struct {
	Message string
}

func (e *ToolCallError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "remote MCP tool failed"
	}
	return e.Message
}

// NormalizeToolCallResponse converts raw text parts and structured content into
// the canonical CallResponse representation used by MCP callers.
//
// Text parts are concatenated in order. If the combined text is valid JSON, it
// becomes Result directly; otherwise it is marshaled as a JSON string. If no
// text is present, fallbackResult is marshaled into Result. Structured is
// marshaled into Structured when non-nil.
func NormalizeToolCallResponse(textParts []string, structured any, fallbackResult any) (CallResponse, error) {
	if len(textParts) == 0 && !hasStructuredPayload(fallbackResult) {
		return CallResponse{}, errors.New("tool returned no content")
	}

	var result jsontext.Value
	textResult := strings.Join(textParts, "")
	textBytes := []byte(textResult)

	switch {
	case textResult != "" && jsontext.Value(textBytes).IsValid():
		result = append(jsontext.Value(nil), textBytes...)
	case textResult != "" && shouldUseStructuredFallback(fallbackResult):
		marshaled, err := json.Marshal(fallbackResult)
		if err != nil {
			return CallResponse{}, fmt.Errorf("failed to marshal fallback content: %w", err)
		}
		result = marshaled
	case textResult != "":
		marshaled, err := json.Marshal(textResult)
		if err != nil {
			return CallResponse{}, fmt.Errorf("failed to marshal text content: %w", err)
		}
		result = marshaled
	default:
		marshaled, err := json.Marshal(fallbackResult)
		if err != nil {
			return CallResponse{}, fmt.Errorf("failed to marshal fallback content: %w", err)
		}
		result = marshaled
	}

	var structuredPayload jsontext.Value
	if hasStructuredPayload(structured) {
		marshaled, err := json.Marshal(structured)
		if err != nil {
			return CallResponse{}, fmt.Errorf("failed to marshal structured content: %w", err)
		}
		structuredPayload = append(jsontext.Value(nil), marshaled...)
	}

	return CallResponse{
		Result:     result,
		Structured: structuredPayload,
	}, nil
}

func hasStructuredPayload(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	//nolint:exhaustive // Only nil-capable kinds need IsNil checks here.
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return false
		}
	}
	//nolint:exhaustive // Other non-nil kinds are intentionally treated as structured payloads.
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() > 0
	default:
		return true
	}
}

func shouldUseStructuredFallback(v any) bool {
	switch v.(type) {
	case nil, string, *string, []byte, jsontext.Value:
		return false
	default:
		return true
	}
}

// ToolCallErrorFromResponse converts an MCP isError content payload into a Go
// error while preserving the compact text message when present.
func ToolCallErrorFromResponse(textParts []string, fallbackResult any) error {
	message := strings.TrimSpace(strings.Join(textParts, ""))
	if message == "" && fallbackResult != nil {
		switch v := fallbackResult.(type) {
		case string:
			message = strings.TrimSpace(v)
		case []byte:
			message = strings.TrimSpace(string(v))
		case jsontext.Value:
			message = strings.TrimSpace(string(v))
		default:
			marshaled, err := json.Marshal(v)
			if err == nil {
				message = strings.TrimSpace(string(marshaled))
			}
		}
	}
	if unquoted, err := strconv.Unquote(message); err == nil {
		message = unquoted
	}
	if message == "" {
		message = "remote MCP tool failed"
	}
	return &ToolCallError{Message: message}
}

// SessionCaller implements Caller by wrapping an MCP SDK ClientSession.
// It is used by transport-specific callers (stdio, HTTP, SSE) to unify tool invocation.
type SessionCaller struct {
	session *mcp.ClientSession
	cancel  context.CancelFunc
}

type connectResult struct {
	session *mcp.ClientSession
	err     error
}

// NewSessionCaller returns a new SessionCaller wrapping the provided SDK session.
func NewSessionCaller(session *mcp.ClientSession, cancel context.CancelFunc) *SessionCaller {
	return &SessionCaller{
		session: session,
		cancel:  cancel,
	}
}

// Close terminates the session and releases resources.
func (c *SessionCaller) Close() error {
	var err error
	if c.cancel != nil {
		defer c.cancel()
	}
	if c.session != nil {
		err = c.session.Close()
	}
	return err
}

// CallTool invokes tools/call over the transport using the SDK session.
func (c *SessionCaller) CallTool(ctx context.Context, req CallRequest) (CallResponse, error) {
	var args any
	if len(req.Payload) > 0 {
		args = req.Payload
	}

	params := &mcp.CallToolParams{
		Name:      req.Tool,
		Arguments: args,
	}

	addTraceMeta(ctx, &params.Meta)

	res, err := c.session.CallTool(ctx, params)
	if err != nil {
		return CallResponse{}, err
	}

	return normalizeSDKToolResult(res)
}

func normalizeSDKToolResult(res *mcp.CallToolResult) (CallResponse, error) {
	if res == nil {
		return CallResponse{}, errors.New("empty MCP response")
	}
	if len(res.Content) == 0 && !hasStructuredPayload(res.StructuredContent) {
		return CallResponse{}, errors.New("tool returned no content")
	}

	textParts := make([]string, 0, len(res.Content))
	structured := make([]any, 0, len(res.Content))

	for _, item := range res.Content {
		structured = append(structured, item)
		if textContent, ok := item.(*mcp.TextContent); ok {
			textParts = append(textParts, textContent.Text)
		}
	}
	var fallback any
	switch {
	case hasStructuredPayload(res.StructuredContent):
		fallback = res.StructuredContent
	case len(textParts) == 0:
		fallback = res.Content[0]
	}
	if res.IsError {
		return CallResponse{}, ToolCallErrorFromResponse(textParts, fallback)
	}

	return NormalizeToolCallResponse(textParts, structured, fallback)
}

// connectSession establishes an SDK session without tying the live session
// lifecycle to a short-lived initialization timeout context.
func connectSession(
	ctx context.Context,
	initTimeout time.Duration,
	connect func(context.Context) (*mcp.ClientSession, error),
) (*SessionCaller, error) {
	sessionCtx, sessionCancel := context.WithCancel(context.WithoutCancel(ctx))
	initCtx, stopInit := initializationContext(ctx, sessionCtx)
	defer stopInit()
	if initTimeout <= 0 {
		session, err := connect(initCtx)
		if err != nil {
			closeSession(session)
			sessionCancel()
			return nil, err
		}
		return NewSessionCaller(session, sessionCancel), nil
	}

	resultCh := make(chan connectResult, 1)
	go func() {
		session, err := connect(initCtx)
		resultCh <- connectResult{session: session, err: err}
	}()

	timer := time.NewTimer(initTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		sessionCancel()
		drainLateConnectResult(resultCh, sessionCancel)
		return nil, ctx.Err()
	case <-timer.C:
		sessionCancel()
		drainLateConnectResult(resultCh, sessionCancel)
		return nil, fmt.Errorf("mcp initialize timed out after %s", initTimeout)
	case result := <-resultCh:
		if result.err != nil {
			closeSession(result.session)
			sessionCancel()
			return nil, result.err
		}
		return NewSessionCaller(result.session, sessionCancel), nil
	}
}

func initializationContext(parent context.Context, sessionCtx context.Context) (context.Context, context.CancelFunc) {
	initCtx, cancelInit := context.WithCancel(sessionCtx)
	stop := make(chan struct{})
	go func() {
		select {
		case <-parent.Done():
			cancelInit()
		case <-sessionCtx.Done():
			cancelInit()
		case <-stop:
		}
	}()
	return initCtx, func() {
		close(stop)
	}
}

func drainLateConnectResult(resultCh <-chan connectResult, cancel context.CancelFunc) {
	go func() {
		result := <-resultCh
		closeSession(result.session)
		cancel()
	}()
}

func closeSession(session *mcp.ClientSession) {
	if session != nil {
		_ = session.Close()
	}
}
