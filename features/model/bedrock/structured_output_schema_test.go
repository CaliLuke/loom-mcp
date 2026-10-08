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

func TestNormalizeBedrockSchemaPreservesIntegerPrecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		want   []string
	}{
		{
			name:   "const",
			schema: `{"type":"integer","const":9007199254740993}`,
			want:   []string{`"const":9007199254740993`},
		},
		{
			name:   "enum",
			schema: `{"type":"integer","enum":[9007199254740993,9007199254740994]}`,
			want:   []string{`"enum":[9007199254740993,9007199254740994]`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizeStructuredOutputSchemaForBedrock([]byte(tc.schema))
			require.NoError(t, err)
			for _, token := range tc.want {
				assert.Contains(t, string(out), token)
			}
			assert.NotContains(t, string(out), `9007199254740992`)
		})
	}
}

func TestNormalizeBedrockSchemaKeepsSupportedMinItemsNumbers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "zero", value: "0", want: `"minItems":0`},
		{name: "one", value: "1", want: `"minItems":1`},
		{name: "decimal one", value: "1.0", want: `"minItems":1.0`},
		{name: "exponent one", value: "1e0", want: `"minItems":1e0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := []byte(`{"type":"array","minItems":` + tc.value + `}`)
			out, err := normalizeStructuredOutputSchemaForBedrock(schema)
			require.NoError(t, err)
			assert.Contains(t, string(out), tc.want)
		})
	}
}

func TestNormalizeBedrockSchemaStripsUnsupportedMinItemsNumbers(t *testing.T) {
	for _, value := range []string{"2", "1.5", "18446744073709551616"} {
		t.Run(value, func(t *testing.T) {
			schema := []byte(`{"type":"array","minItems":` + value + `}`)
			out, err := normalizeStructuredOutputSchemaForBedrock(schema)
			require.NoError(t, err)
			assert.NotContains(t, string(out), `"minItems"`)
		})
	}
}

func TestNormalizeBedrockSchemaRejectsInvalidJSON(t *testing.T) {
	for _, raw := range []string{`{"type":"integer","const":1,"const":2}`, `{"type":"integer"} {}`, "{\"description\":\"\xff\"}", `{"type":"integer","const":`} {
		_, err := normalizeStructuredOutputSchemaForBedrock([]byte(raw))
		require.Error(t, err, raw)
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
