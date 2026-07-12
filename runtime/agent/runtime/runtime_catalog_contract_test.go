package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestRuntimeCatalogListsAreDeterministic(t *testing.T) {
	rt := New()
	rt.agents = map[agent.Ident]AgentRegistration{
		"svc.zeta":  {},
		"svc.alpha": {},
		"svc.gamma": {},
		"svc.beta":  {},
	}
	rt.toolsets = map[string]ToolsetRegistration{
		"tools.zeta":  {},
		"tools.alpha": {},
		"tools.gamma": {},
		"tools.beta":  {},
	}

	assert.Equal(t, []agent.Ident{"svc.alpha", "svc.beta", "svc.gamma", "svc.zeta"}, rt.ListAgents())
	assert.Equal(t, []string{"tools.alpha", "tools.beta", "tools.gamma", "tools.zeta"}, rt.ListToolsets())
	empty := &Runtime{agents: map[agent.Ident]AgentRegistration{}, toolsets: map[string]ToolsetRegistration{}}
	assert.Nil(t, empty.ListAgents())
	assert.Nil(t, empty.ListToolsets())
}

func TestRuntimeCatalogDetachesRegisteredAndReturnedToolSpecs(t *testing.T) {
	rt := New()
	agentID := agent.Ident("svc.agent")
	spec := catalogToolSpec()
	require.NoError(t, rt.storeRegisteredAgent(AgentRegistration{ID: agentID, Specs: []tools.ToolSpec{spec}}))

	mutateCatalogToolSpec(&spec)
	assertCatalogToolSpec(t, mustCatalogToolSpec(t, rt, agentID))

	returned := mustCatalogToolSpec(t, rt, agentID)
	mutateCatalogToolSpec(&returned)
	assertCatalogToolSpec(t, mustCatalogToolSpec(t, rt, agentID))

	byName, ok := rt.ToolSpec("tools.search")
	require.True(t, ok)
	mutateCatalogToolSpec(&byName)
	byName, ok = rt.ToolSpec("tools.search")
	require.True(t, ok)
	assertCatalogToolSpec(t, byName)
}

func TestRuntimeCatalogToolSchemasAndModels(t *testing.T) {
	rt := New()
	rt.mu.Lock()
	rt.addToolSpecsLocked([]tools.ToolSpec{
		{Name: "tools.valid", Payload: tools.TypeSpec{Schema: []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`)}},
		{Name: "tools.invalid", Payload: tools.TypeSpec{Schema: []byte(`{`)}},
		{Name: "tools.empty"},
	})
	rt.mu.Unlock()

	schema, ok := rt.ToolSchema("tools.valid")
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])
	schema["properties"].(map[string]any)["query"] = "mutated"
	schema, ok = rt.ToolSchema("tools.valid")
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "string"}, schema["properties"].(map[string]any)["query"])
	_, ok = rt.ToolSchema("tools.invalid")
	assert.False(t, ok)
	_, ok = rt.ToolSchema("tools.empty")
	assert.False(t, ok)
	_, ok = rt.ToolSchema("tools.missing")
	assert.False(t, ok)

	require.EqualError(t, rt.RegisterModel("", contractModelClient{}), "model id is required")
	require.EqualError(t, rt.RegisterModel("default", nil), "model client is required")
	client := contractModelClient{}
	require.NoError(t, rt.RegisterModel("default", client))
	got, ok := rt.ModelClient("default")
	require.True(t, ok)
	assert.Equal(t, client, got)
	_, ok = rt.ModelClient("")
	assert.False(t, ok)
	_, ok = rt.ModelClient("missing")
	assert.False(t, ok)
}

func catalogToolSpec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "tools.search",
		Tags: []string{"search"},
		Meta: map[string][]string{"audience": {"operator"}},
		Bounds: &tools.BoundsSpec{Paging: &tools.PagingSpec{
			CursorField:     "cursor",
			NextCursorField: "next_cursor",
		}},
		ServerData: []*tools.ServerDataSpec{{
			Kind: "card",
			Type: tools.TypeSpec{
				Schema:       []byte(`{"type":"object"}`),
				ExampleJSON:  []byte(`{"title":"Done"}`),
				ExampleInput: map[string]any{"nested": map[string]any{"title": "Done"}},
			},
		}},
		Confirmation: &tools.ConfirmationSpec{Title: "Confirm search"},
		Payload: tools.TypeSpec{
			Schema:       []byte(`{"type":"object"}`),
			ExampleJSON:  []byte(`{"query":"status"}`),
			ExampleInput: map[string]any{"nested": []any{"status", []byte("bytes")}},
		},
		Result: tools.TypeSpec{Schema: []byte(`{"type":"object"}`)},
	}
}

func mutateCatalogToolSpec(spec *tools.ToolSpec) {
	spec.Tags[0] = "mutated"
	spec.Meta["audience"][0] = "mutated"
	spec.Bounds.Paging.CursorField = "mutated"
	spec.ServerData[0].Kind = "mutated"
	spec.ServerData[0].Type.Schema[0] = '['
	spec.ServerData[0].Type.ExampleJSON[0] = '['
	spec.ServerData[0].Type.ExampleInput["nested"].(map[string]any)["title"] = "mutated"
	spec.Confirmation.Title = "mutated"
	spec.Payload.Schema[0] = '['
	spec.Payload.ExampleJSON[0] = '['
	spec.Payload.ExampleInput["nested"].([]any)[0] = "mutated"
	spec.Payload.ExampleInput["nested"].([]any)[1].([]byte)[0] = 'X'
	spec.Result.Schema[0] = '['
}

func mustCatalogToolSpec(t *testing.T, rt *Runtime, agentID agent.Ident) tools.ToolSpec {
	t.Helper()
	specs := rt.ToolSpecsForAgent(agentID)
	require.Len(t, specs, 1)
	return specs[0]
}

func assertCatalogToolSpec(t *testing.T, spec tools.ToolSpec) {
	t.Helper()
	assert.Equal(t, []string{"search"}, spec.Tags)
	assert.Equal(t, []string{"operator"}, spec.Meta["audience"])
	require.NotNil(t, spec.Bounds)
	require.NotNil(t, spec.Bounds.Paging)
	assert.Equal(t, "cursor", spec.Bounds.Paging.CursorField)
	require.Len(t, spec.ServerData, 1)
	require.NotNil(t, spec.ServerData[0])
	assert.Equal(t, "card", spec.ServerData[0].Kind)
	assert.JSONEq(t, `{"type":"object"}`, string(spec.ServerData[0].Type.Schema))
	assert.JSONEq(t, `{"title":"Done"}`, string(spec.ServerData[0].Type.ExampleJSON))
	assert.Equal(t, "Done", spec.ServerData[0].Type.ExampleInput["nested"].(map[string]any)["title"])
	require.NotNil(t, spec.Confirmation)
	assert.Equal(t, "Confirm search", spec.Confirmation.Title)
	assert.JSONEq(t, `{"type":"object"}`, string(spec.Payload.Schema))
	assert.JSONEq(t, `{"query":"status"}`, string(spec.Payload.ExampleJSON))
	assert.Equal(t, "status", spec.Payload.ExampleInput["nested"].([]any)[0])
	assert.Equal(t, []byte("bytes"), spec.Payload.ExampleInput["nested"].([]any)[1])
	assert.JSONEq(t, `{"type":"object"}`, string(spec.Result.Schema))
}
