package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
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
	require.Contains(t, provider, "func DispatchByIDMethod(ctx context.Context, meta *runtime.ToolCallMeta, raw json.RawMessage, labels map[string]string, opts ByIDDispatchOptions) (*planner.ToolResult, error)")
}

func TestGeneratedMethodBackedDispatcherPreservesExecutorHooks(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelf())

	executor := generatedContentBySuffix(t, files, "agents/scribe/lookup/service_executor.go")

	require.Contains(t, executor, "WithPayloadMapper")
	require.Contains(t, executor, "WithResultMapper")
	require.Contains(t, executor, "WithInterceptors")
	require.Contains(t, executor, "lookup.DispatchByIDMethod(ctx, meta, json.RawMessage(call.Payload), call.Labels, lookup.ByIDDispatchOptions{")
	require.Contains(t, executor, "Call: caller,")
	require.Contains(t, executor, "MapPayload: cfg.mapPayload,")
	require.Contains(t, executor, "MapResult: cfg.mapResult,")
	require.Contains(t, executor, "Injectors: dispatchInjectors(cfg.injectors),")
	require.NotContains(t, executor, "lookup.InitByIDMethodPayload")
	require.NotContains(t, executor, "lookup.InitByIDToolResult")
}
