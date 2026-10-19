package discoverybench

import (
	"context"
	"encoding/json/v2"
	"errors"

	assistantapi "example.com/assistant"
	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// exportCatalog observes the generated server result before generic client JSON
// decoding can round int64/uint64 schema bounds through floating point values.
func exportCatalog(ctx context.Context) (data []byte, err error) {
	server, err := mcpassistant.NewSDKServer(assistantapi.NewAssistant(), nil)
	if err != nil {
		return nil, err
	}
	captured := make(chan []byte, 1)
	server.Server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			result, callErr := next(ctx, method, req)
			if callErr == nil && method == "tools/list" {
				raw, marshalErr := json.Marshal(result, json.Deterministic(true))
				if marshalErr != nil {
					return nil, marshalErr
				}
				captured <- raw
			}
			return result, callErr
		}
	})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, serverSession.Close())
	}()
	client := sdk.NewClient(&sdk.Implementation{Name: "discovery-benchmark", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, session.Close())
	}()
	if _, err = session.ListTools(ctx, nil); err != nil {
		return nil, err
	}
	return <-captured, nil
}

func registrationFor(service assistant.Service) (agentsruntime.ToolsetRegistration, error) {
	adapter := mcpassistant.NewMCPAdapter(service, nil, &mcpassistant.MCPAdapterOptions{ToolSearch: &mcpassistant.ToolSearchOptions{}})
	return mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
}

func invoke(ctx context.Context, registration agentsruntime.ToolsetRegistration, name string, payload []byte) (*planner.ToolResult, error) {
	request := &planner.ToolRequest{Name: tools.Ident(name)}
	if err := request.Payload.UnmarshalJSON(payload); err != nil {
		return nil, err
	}
	result, err := registration.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || result.ToolResult == nil {
		return nil, errors.New("generated executor returned no tool result")
	}
	return result.ToolResult, nil
}
