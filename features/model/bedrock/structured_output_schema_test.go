package bedrock

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBedrockSchemaRecursesThroughEverySchemaKeyword(t *testing.T) {
	cases := []struct {
		keyword string
		shape   string
		child   func(map[string]any) map[string]any
	}{
		{keyword: "if", shape: "single", child: singleSchemaChild("if")},
		{keyword: "then", shape: "single", child: singleSchemaChild("then")},
		{keyword: "else", shape: "single", child: singleSchemaChild("else")},
		{keyword: "propertyNames", shape: "single", child: singleSchemaChild("propertyNames")},
		{keyword: "contains", shape: "single", child: singleSchemaChild("contains")},
		{keyword: "not", shape: "single", child: singleSchemaChild("not")},
		{keyword: "dependentSchemas", shape: "named", child: namedSchemaChild("dependentSchemas")},
		{keyword: "prefixItems", shape: "list", child: listSchemaChild("prefixItems")},
		{keyword: "allOf", shape: "list", child: listSchemaChild("allOf")},
		{keyword: "anyOf", shape: "list", child: listSchemaChild("anyOf")},
	}

	for _, tc := range cases {
		t.Run(tc.keyword, func(t *testing.T) {
			doc := map[string]any{tc.keyword: nestedBedrockObjectSchema()}
			switch tc.shape {
			case "named":
				doc[tc.keyword] = map[string]any{"entry": nestedBedrockObjectSchema()}
			case "list":
				doc[tc.keyword] = []any{nestedBedrockObjectSchema()}
			}
			raw, err := json.Marshal(doc)
			require.NoError(t, err)

			normalized, err := normalizeStructuredOutputSchemaForBedrock(raw)
			require.NoError(t, err)

			var got map[string]any
			require.NoError(t, json.Unmarshal(normalized, &got))
			child := tc.child(got)
			assert.NotContains(t, child, "title")
			assert.Equal(t, false, child["additionalProperties"])
			properties, ok := child[bedrockSchemaProperties].(map[string]any)
			require.True(t, ok)
			value, ok := properties["value"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, value, "pattern")
		})
	}
}

func nestedBedrockObjectSchema() map[string]any {
	return map[string]any{
		"type":  "object",
		"title": "nested",
		bedrockSchemaProperties: map[string]any{
			"value": map[string]any{"type": "string", "pattern": "unsupported"},
		},
	}
}

func singleSchemaChild(keyword string) func(map[string]any) map[string]any {
	return func(doc map[string]any) map[string]any {
		return doc[keyword].(map[string]any)
	}
}

func namedSchemaChild(keyword string) func(map[string]any) map[string]any {
	return func(doc map[string]any) map[string]any {
		return doc[keyword].(map[string]any)["entry"].(map[string]any)
	}
}

func listSchemaChild(keyword string) func(map[string]any) map[string]any {
	return func(doc map[string]any) map[string]any {
		return doc[keyword].([]any)[0].(map[string]any)
	}
}
