package runtime

// tool_unavailable.go defines the runtime-owned "tool unavailable" tool.
//
// This tool is the canonical representation of "the model requested a tool name
// that is not registered for this run". We keep the transcript/tool handshake
// structurally valid while retaining only the exact available catalog, never
// the rejected model-authored name or payload.

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const toolUnavailableToolsetName = "loom-mcp.runtime"

type toolUnavailablePayload struct {
	AvailableTools []string `json:"available_tools"`
}

// JSON schema field keys and value tokens reused across the tool-unavailable
// schema. Lifted to constants to satisfy goconst on the embedded JSON-schema
// literals.
const (
	availableToolsKey       = "available_tools"
	jsonSchemaTypeKey       = "type"
	jsonSchemaTypeValue     = "object"
	jsonSchemaPropertiesKey = "properties"
	jsonSchemaString        = "string"
	jsonSchemaArray         = "array"
)

func toolUnavailableToolDefinition() *model.ToolDefinition {
	return &model.ToolDefinition{
		Name:        tools.ToolUnavailable.String(),
		Description: "Internal. Used when the model requests an unknown tool name. Always returns an error with a retry hint to pick a tool from the advertised list.",
		InputSchema: map[string]any{
			jsonSchemaTypeKey: jsonSchemaTypeValue,
			jsonSchemaPropertiesKey: map[string]any{
				availableToolsKey: map[string]any{
					jsonSchemaTypeKey: jsonSchemaArray,
					"items":           map[string]any{jsonSchemaTypeKey: jsonSchemaString},
					"description":     "Exact tool names advertised for the rejected planner turn.",
				},
			},
			"required":             []string{availableToolsKey},
			"additionalProperties": false,
		},
	}
}

func toolUnavailableToolsetRegistration() ToolsetRegistration {
	spec := tools.ToolSpec{
		Name:        tools.ToolUnavailable,
		Service:     "loom-mcp",
		Toolset:     toolUnavailableToolsetName,
		Description: "Runtime-owned tool that represents unknown tool calls.",
		Payload: tools.TypeSpec{
			Name:        "ToolUnavailablePayload",
			Schema:      mustMarshalToolUnavailableSchema(),
			ExampleJSON: []byte(`{"available_tools":["svc_read_events"]}`),
			Codec:       tools.AnyJSONCodec,
		},
		Result: tools.TypeSpec{
			Name:   "ToolUnavailableResult",
			Schema: []byte(`{"type":"object","additionalProperties":true}`),
			Codec:  tools.AnyJSONCodec,
		},
	}
	return ToolsetRegistration{
		Name:         toolUnavailableToolsetName,
		Description:  "loom-mcp runtime internal tools",
		Inline:       true,
		DispatchMode: DispatchInline,
		Execute:      executeToolUnavailable,
		Specs:        []tools.ToolSpec{spec},
	}
}

func mustMarshalToolUnavailableSchema() []byte {
	schema := toolUnavailableToolDefinition().InputSchema
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("runtime: marshal tool_unavailable schema: %w", err))
	}
	return data
}

func executeToolUnavailable(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	var payload toolUnavailablePayload
	if len(call.Payload) > 0 {
		if !decodeToolUnavailablePayload(call.Payload, &payload) {
			// This tool is runtime-owned but models can still call it directly.
			// Treat malformed payloads as tool errors so the run can continue.
			toolErr := planner.NewToolError("tool_unavailable payload is invalid")
			return Executed(&planner.ToolResult{
				Name:       call.Name,
				ToolCallID: call.ToolCallID,
				Error:      toolErr,
				RetryHint: BoundGeneratedRetryHint(&planner.RetryHint{
					Reason:         planner.RetryReasonInvalidArguments,
					Tool:           call.Name,
					Message:        "Call tool_unavailable with the exact available_tools list.",
					RestrictToTool: true,
				}),
			}), nil
		}
	}

	toolErr := planner.NewToolError("model requested a tool that is unavailable for this run")
	message := "Tool name is not registered for this run. Choose one of the advertised tools."
	if len(payload.AvailableTools) > 0 {
		message = "Choose one of these exact advertised tool names: " + strings.Join(payload.AvailableTools, ", ")
	}
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      toolErr,
		RetryHint: BoundGeneratedRetryHint(&planner.RetryHint{
			Reason:         planner.RetryReasonToolUnavailable,
			Tool:           call.Name,
			RestrictToTool: false,
			Message:        message,
		}),
	}), nil
}

// decodeToolUnavailablePayload reports failure without exposing parser details
// that can include model-supplied payload fragments.
func decodeToolUnavailablePayload(data []byte, payload *toolUnavailablePayload) bool {
	return json.Unmarshal(data, payload) == nil
}

func (r *Runtime) rewriteUnknownToolCalls(calls []planner.ToolRequest, catalog toolPolicyEnvelope) []planner.ToolRequest {
	if len(calls) == 0 {
		return nil
	}

	available := r.recoveryToolCatalog(catalog)
	var rejected []planner.ToolRequest
	for _, call := range calls {
		if call.Name == tools.ToolUnavailable {
			if !r.isCanonicalToolUnavailablePayload(call.Payload, catalog) {
				call.Payload = marshalToolUnavailablePayload(available)
			}
			rejected = append(rejected, call)
			continue
		}
		if _, ok := r.toolSpec(call.Name); ok && toolAllowedByEnvelope(call.Name, catalog) {
			continue
		}
		call.Name = tools.ToolUnavailable
		call.Payload = marshalToolUnavailablePayload(available)
		rejected = append(rejected, call)
	}
	if len(rejected) > 0 {
		return rejected
	}
	return calls
}

func exactToolUnavailablePayload(request *model.Request) rawjson.Message {
	names := recoveryToolNames(request)
	available := make([]string, 0, len(names))
	for _, name := range names {
		if name != tools.ToolUnavailable.String() {
			available = append(available, name)
		}
	}
	return marshalToolUnavailablePayload(available)
}

func marshalToolUnavailablePayload(available []string) rawjson.Message {
	payload, err := json.Marshal(toolUnavailablePayload{AvailableTools: available})
	if err != nil {
		panic(fmt.Errorf("runtime: encode tool_unavailable payload: %w", err))
	}
	return rawjson.Message(payload)
}

func (r *Runtime) isCanonicalToolUnavailablePayload(data rawjson.Message, catalog toolPolicyEnvelope) bool {
	var payload toolUnavailablePayload
	if !decodeToolUnavailablePayload(data, &payload) || !slices.IsSorted(payload.AvailableTools) {
		return false
	}
	for index, name := range payload.AvailableTools {
		ident := tools.Ident(name)
		if name == "" || ident == tools.ToolUnavailable || (index > 0 && payload.AvailableTools[index-1] == name) {
			return false
		}
		if _, ok := r.toolSpec(ident); !ok || !toolAllowedByEnvelope(ident, catalog) {
			return false
		}
	}
	return bytes.Equal(data, marshalToolUnavailablePayload(payload.AvailableTools))
}

func (r *Runtime) recoveryToolCatalog(catalog toolPolicyEnvelope) []string {
	if catalog.Active {
		out := make([]string, 0, len(catalog.Allowed))
		for _, name := range catalog.Allowed {
			if name == tools.ToolUnavailable {
				continue
			}
			out = append(out, name.String())
		}
		slices.Sort(out)
		return out
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.toolSpecs))
	for name := range r.toolSpecs {
		if name != tools.ToolUnavailable {
			out = append(out, name.String())
		}
	}
	slices.Sort(out)
	return out
}

func toolAllowedByEnvelope(name tools.Ident, catalog toolPolicyEnvelope) bool {
	if !catalog.Active {
		return true
	}
	for _, allowed := range catalog.Allowed {
		if name == allowed {
			return true
		}
	}
	return false
}
