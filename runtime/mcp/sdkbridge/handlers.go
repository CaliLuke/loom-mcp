package sdkbridge

import (
	"context"

	"encoding/json/jsontext"
	json "encoding/json/v2"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkclient"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandlerContext configures common request-to-runtime context plumbing.
type HandlerContext struct {
	RequestStateKey []byte
	Sessions        *SessionState
}

// ToolRequest contains SDK tool input and a typed service-payload context binder.
type ToolRequest struct {
	Name      string
	Arguments jsontext.Value
	Bind      func(any) context.Context
}

// PromptRequest contains SDK prompt input and a typed service-payload context binder.
type PromptRequest struct {
	Name      string
	Arguments jsontext.Value
	Bind      func(any) context.Context
}

// ResourceRequest contains SDK resource input and a typed service-payload context binder.
type ResourceRequest struct {
	URI  string
	Bind func(any) context.Context
}

// ToolCall executes generated typed tool dispatch.
type ToolCall func(context.Context, ToolRequest) (*mcpsdk.CallToolResult, error)

// PromptCall executes generated typed prompt dispatch.
type PromptCall func(context.Context, PromptRequest) (*mcpsdk.GetPromptResult, error)

// ResourceCall executes generated typed resource dispatch.
type ResourceCall func(context.Context, ResourceRequest) (*mcpsdk.ReadResourceResult, error)

// ToolHandler adapts official SDK requests to a generated typed tool closure.
func ToolHandler(config HandlerContext, call ToolCall) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var inputResponses mcpsdk.InputResponseMap
		var name, requestState string
		var arguments jsontext.Value
		if req != nil && req.Params != nil {
			name = req.Params.Name
			arguments = req.Params.Arguments
			inputResponses = req.Params.InputResponses
			requestState = req.Params.RequestState
		}
		if req != nil && req.Params != nil {
			ctx = mcpruntime.WithProgressToken(ctx, req.Params.GetProgressToken())
		}
		bind := requestBinder(ctx, requestSession(req), config, inputResponses, requestState, "tools/call")
		result, err := call(ctx, ToolRequest{Name: name, Arguments: arguments, Bind: bind})
		if requests, state, ok := sdkclient.InputRequired(err); ok {
			return &mcpsdk.CallToolResult{InputRequests: requests, RequestState: state}, nil
		}
		return result, err
	}
}

// PromptHandler adapts official SDK requests to a generated typed prompt closure.
func PromptHandler(config HandlerContext, call PromptCall) mcpsdk.PromptHandler {
	return func(ctx context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		var inputResponses mcpsdk.InputResponseMap
		var name, requestState string
		var arguments jsontext.Value
		if req != nil && req.Params != nil {
			name = req.Params.Name
			inputResponses = req.Params.InputResponses
			requestState = req.Params.RequestState
			if req.Params.Arguments != nil {
				encoded, err := json.Marshal(req.Params.Arguments)
				if err != nil {
					return nil, err
				}
				arguments = encoded
			}
		}
		bind := requestBinder(ctx, requestSession(req), config, inputResponses, requestState, "prompts/get")
		result, err := call(ctx, PromptRequest{Name: name, Arguments: arguments, Bind: bind})
		if requests, state, ok := sdkclient.InputRequired(err); ok {
			return &mcpsdk.GetPromptResult{InputRequests: requests, RequestState: state}, nil
		}
		return result, err
	}
}

// ResourceHandler adapts official SDK requests to a generated typed resource closure.
func ResourceHandler(config HandlerContext, call ResourceCall) mcpsdk.ResourceHandler {
	return func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		var inputResponses mcpsdk.InputResponseMap
		var uri, requestState string
		if req != nil && req.Params != nil {
			uri = req.Params.URI
			inputResponses = req.Params.InputResponses
			requestState = req.Params.RequestState
		}
		bind := requestBinder(ctx, requestSession(req), config, inputResponses, requestState, "resources/read")
		result, err := call(ctx, ResourceRequest{URI: uri, Bind: bind})
		if requests, state, ok := sdkclient.InputRequired(err); ok {
			return &mcpsdk.ReadResourceResult{InputRequests: requests, RequestState: state}, nil
		}
		return result, err
	}
}

// BindCompletionContext adds common official SDK client features to completion dispatch.
func BindCompletionContext(ctx context.Context, req *mcpsdk.CompleteRequest, config HandlerContext) context.Context {
	if req == nil {
		return ctx
	}
	return requestBinder(ctx, req.GetSession(), config, nil, "", "completion/complete")(req.Params)
}

func requestBinder(ctx context.Context, session mcpsdk.Session, config HandlerContext, inputResponses mcpsdk.InputResponseMap, requestState, method string) func(any) context.Context {
	return func(params any) context.Context {
		bound := ctx
		if session == nil {
			config.Sessions.MarkInitialized("")
			return bound
		}
		if serverSession, ok := session.(*mcpsdk.ServerSession); ok && serverSession != nil {
			bound = sdkclient.WithClientFeatures(bound, serverSession, sdkclient.ClientFeaturesOptions{
				InputResponses: inputResponses, RequestMethod: method, RequestParams: params,
				RequestState: requestState, RequestStateKey: config.RequestStateKey,
			})
		}
		sessionID := session.ID()
		config.Sessions.MarkInitialized(sessionID)
		if sessionID == "" {
			return bound
		}
		return mcpruntime.WithSessionID(bound, sessionID)
	}
}

func requestSession[T interface {
	comparable
	GetSession() mcpsdk.Session
}](req T) mcpsdk.Session {
	var zero T
	if req == zero {
		return nil
	}
	session := req.GetSession()
	if sdkSession, ok := session.(*mcpsdk.ServerSession); ok && sdkSession == nil {
		return nil
	}
	return session
}
