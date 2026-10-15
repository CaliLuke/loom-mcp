package openai_test

import (
	"encoding/json/v2"
	"testing"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictToolsRestoreNullAbsenceAcrossReferencesAndArrays(t *testing.T) {
	schema := `{"type":"object","$defs":{"a/b~c":{"type":"object","properties":{"optional":{"type":"string"},"nullable":{"enum":["x",null]}}}},"properties":{"records":{"type":"array","items":{"$ref":"#/$defs/a~1b~0c"}},"choice":{"oneOf":[{"type":"string"},{"type":"integer"}]},"ref":{"$ref":"#/$defs/a~1b~0c"}}}`
	var inputSchema map[string]any
	require.NoError(t, json.Unmarshal([]byte(schema), &inputSchema))
	mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{Type: "function_call", Name: "lookup", CallID: "call", Arguments: responses.ResponseOutputItemUnionArguments{OfString: `{"records":[{"optional":null,"nullable":null}],"choice":null,"ref":{"optional":null,"nullable":"x"}}`}}}}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "test"})
	require.NoError(t, err)
	result, err := client.Complete(t.Context(), &model.Request{Tools: []*model.ToolDefinition{{Name: "lookup", InputSchema: inputSchema}}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}}}})
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.JSONEq(t, `{"records":[{"nullable":null}],"ref":{"nullable":"x"}}`, string(result.ToolCalls[0].Payload))
	projected, err := json.Marshal(mock.captured.Tools[0].OfFunction.Parameters)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"object","additionalProperties":false,"required":["choice","records","ref"],"$defs":{"a/b~c":{"type":"object","additionalProperties":false,"required":["nullable","optional"],"properties":{"optional":{"type":["string","null"]},"nullable":{"enum":["x",null]}}}},"properties":{"records":{"type":["array","null"],"items":{"$ref":"#/$defs/a~1b~0c"}},"choice":{"anyOf":[{"type":"string"},{"type":"integer"},{"type":"null"}]},"ref":{"anyOf":[{"$ref":"#/$defs/a~1b~0c"},{"type":"null"}]}}}`, string(projected))
	// Projection must not mutate the registered canonical schema.
	original, err := json.Marshal(inputSchema)
	require.NoError(t, err)
	assert.JSONEq(t, schema, string(original))
}

func TestStrictToolSchemasRejectNestedOpenMapsBeforeProviderCall(t *testing.T) {
	for _, tc := range []struct{ name, schema, path string }{
		{"array", `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"object","additionalProperties":true}}}}`, "items"},
		{"union", `{"type":"object","properties":{"data":{"anyOf":[{"type":"object","additionalProperties":{"type":"string"}},{"type":"null"}]}}}`, "anyOf"},
		{"definitions", `{"type":"object","$defs":{"bad":{"type":"object","additionalProperties":true}}}`, "$defs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.schema), &schema))
			mock := &mockResponsesClient{response: &responses.Response{}}
			client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Tools: []*model.ToolDefinition{{Name: "lookup", InputSchema: schema}}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}}}})
			require.ErrorContains(t, err, tc.path)
			assert.Empty(t, mock.captured.Model, "invalid contracts must not reach the provider")
		})
	}
}
