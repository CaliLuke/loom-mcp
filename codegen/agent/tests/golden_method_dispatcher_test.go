package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGeneratedMethodBackedDispatcher(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelf())

	provider := fileContent(t, files, "gen/alpha/toolsets/lookup/provider.go")

	require.Contains(t, provider, "type ByIDDispatchOptions struct")
	require.Contains(t, provider, "Call       func(context.Context, any) (any, error)")
	require.Contains(t, provider, "MapPayload func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)")
	require.Contains(t, provider, "MapResult  func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)")
	require.Contains(t, provider, "Injectors  []func(context.Context, any, *runtime.ToolCallMeta) error")
	require.Contains(t, provider, "func DispatchByIDMethod(ctx context.Context, meta *runtime.ToolCallMeta, raw jsontext.Value, labels map[string]string, opts ByIDDispatchOptions) (*planner.ToolResult, error)")
}

// TestGeneratedMethodBackedDispatcherDecodesOmittedArguments verifies that the
// generated dispatcher decodes an empty JSON object when the raw arguments are
// omitted (legal per MCP) instead of type-asserting a nil interface, so
// required-field validation errors surface as tool errors rather than panics.
func TestGeneratedMethodBackedDispatcherDecodesOmittedArguments(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelf())

	provider := fileContent(t, files, "gen/alpha/toolsets/lookup/provider.go")

	require.Contains(t, provider, "if len(raw) == 0 {")
	require.Contains(t, provider, `raw = jsontext.Value("{}")`)
	require.Contains(t, provider, "decodedArgs, err := ByIDPayloadCodec.FromJSON(raw)")
	require.Contains(t, provider, "toolArgs = decodedArgs")
	require.NotContains(t, provider, "if len(raw) > 0 {",
		"payload decode must be unconditional so toolArgs is never a nil interface")
}

func TestGeneratedMethodBackedDispatcherPreservesExecutorHooks(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelf())

	executor := generatedContentBySuffix(t, files, "agents/scribe/lookup/service_executor.go")

	require.Contains(t, executor, "WithPayloadMapper")
	require.Contains(t, executor, "WithResultMapper")
	require.Contains(t, executor, "WithInterceptors")
	require.Contains(t, executor, "lookup.DispatchByIDMethod(ctx, meta, jsontext.Value(call.Payload), call.Labels, lookup.ByIDDispatchOptions{")
	require.Contains(t, executor, "Call: caller,")
	require.Contains(t, executor, "MapPayload: cfg.mapPayload,")
	require.Contains(t, executor, "MapResult: cfg.mapResult,")
	require.Contains(t, executor, "Injectors: dispatchInjectors(cfg.injectors),")
	require.NotContains(t, executor, "lookup.InitByIDMethodPayload")
	require.NotContains(t, executor, "lookup.InitByIDToolResult")
}
