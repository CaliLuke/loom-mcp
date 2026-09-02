package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_BoundedResult_UsesBoundsSpecAndProjection(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelfBoundedResult())

	specs := generatedContentBySuffix(t, files, "toolsets/lookup/specs.go")
	require.Contains(t, specs, "Bounds: &tools.BoundsSpec{")
	require.Contains(t, specs, "Paging: &tools.PagingSpec{")
	require.NotContains(t, specs, "BoundedResult: true")

	schemas := generatedContentBySuffix(t, files, "agents/scribe/specs/tool_schemas.json")
	require.Contains(t, schemas, `"returned"`)
	require.Contains(t, schemas, `"truncated"`)
	require.Contains(t, schemas, `"total"`)
	require.Contains(t, schemas, `"refinement_hint"`)
	require.Contains(t, schemas, `"next_cursor"`)

	executor := generatedContentBySuffix(t, files, "agents/scribe/lookup/service_executor.go")
	require.Contains(t, executor, "lookup.DispatchSearchMethod(ctx, meta, jsontext.Value(call.Payload), call.Labels, lookup.SearchDispatchOptions{")
	require.Contains(t, executor, "Call: caller,")
	require.Contains(t, executor, "MapPayload: cfg.mapPayload,")
	require.Contains(t, executor, "MapResult: cfg.mapResult,")
	require.Contains(t, executor, "Injectors: dispatchInjectors(cfg.injectors),")
	require.NotContains(t, executor, "initSearchBounds")
	require.NotContains(t, executor, "lookup.InitSearchToolResult")
	require.NotContains(t, executor, `requires method result field "returned"`)
	require.NotContains(t, executor, `requires method result field "truncated"`)

	provider := generatedContentBySuffix(t, files, "toolsets/lookup/provider.go")
	require.Contains(t, provider, "bounds := initSearchBounds(typedMethodOut)")
	require.Regexp(t, `Bounds:\s+bounds,`, provider)
	require.Contains(t, provider, "func initSearchBounds(")
	require.Contains(t, provider, "bounds.Returned = mr.Returned")
	require.Contains(t, provider, "bounds.Truncated = mr.Truncated")
	require.Contains(t, provider, "bounds.NextCursor = mr.NextCursor")
}
