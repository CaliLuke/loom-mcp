package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	mcppkg "{{ .MCPImportPath }}"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
)

// Caller adapts the generated MCP JSON-RPC client to the runtime Caller interface.
type Caller struct {
	suite  string
	client *Client
}

// NewCaller wraps the generated Client so it can register with the loom-mcp runtime.
func NewCaller(client *Client, suite string) mcpruntime.Caller {
	return Caller{suite: suite, client: client}
}

// CallTool invokes tools/call via the generated JSON-RPC client and normalizes the response.
func (c Caller) CallTool(ctx context.Context, req mcpruntime.CallRequest) (mcpruntime.CallResponse, error) {
	if c.client == nil {
		return mcpruntime.CallResponse{}, errors.New("mcp client not configured")
	}
	payload := &mcppkg.ToolsCallPayload{Name: req.Tool, Arguments: json.RawMessage(req.Payload)}
	streamEndpoint := c.client.ToolsCall()
	stream, err := streamEndpoint(ctx, payload)
	if err != nil {
		return mcpruntime.CallResponse{}, err
	}
	clientStream, ok := stream.(*ToolsCallClientStream)
	if !ok {
		return mcpruntime.CallResponse{}, errors.New("invalid tools/call stream type")
	}
	var merged *mcppkg.ToolsCallResult
	eventCount := 0
	for {
		ev, recvErr := clientStream.Recv(ctx)
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return mcpruntime.CallResponse{}, recvErr
		}
		if ev == nil {
			continue
		}
		eventCount++
		if merged == nil {
			merged = &mcppkg.ToolsCallResult{}
		}
		merged.Content = append(merged.Content, ev.Content...)
		if ev.IsError != nil {
			merged.IsError = ev.IsError
		}
	}
	if merged == nil || len(merged.Content) == 0 {
		return mcpruntime.CallResponse{}, fmt.Errorf("empty MCP response for suite %q tool %q: stream ended after %d events with no content", c.suite, req.Tool, eventCount)
	}

	return normalizeToolResult(merged)
}

func normalizeToolResult(last *mcppkg.ToolsCallResult) (mcpruntime.CallResponse, error) {
	textParts := make([]string, 0, len(last.Content))
	for _, item := range last.Content {
		if item.Text != nil {
			textParts = append(textParts, *item.Text)
		}
	}
	var fallback any
	if len(last.Content) > 0 {
		fallback = last.Content[0]
	}
	return mcpruntime.NormalizeToolCallResponse(textParts, last.Content, fallback)
}
