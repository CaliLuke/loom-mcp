package codegen

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectUnionSchemaPreservesBranchAdmission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		schema  string
		valid   []string
		invalid []string
	}{
		{
			name:    "closed branches and distinct property types",
			schema:  `{"oneOf":[{"type":"object","properties":{"action":{"const":"read"},"value":{"type":"integer"}},"required":["action","value"],"additionalProperties":false},{"type":"object","properties":{"action":{"const":"write"},"value":{"type":"string"},"name":{"type":"string"}},"required":["action","value","name"],"additionalProperties":false}]}`,
			valid:   []string{`{"action":"read","value":1}`, `{"action":"write","value":"one","name":"entry"}`},
			invalid: []string{`{"action":"read","value":"one"}`, `{"action":"read","value":1,"name":"entry"}`, `{"action":"write","value":"one"}`},
		},
		{
			name:    "open branch admits undeclared properties",
			schema:  `{"oneOf":[{"type":"object","properties":{"action":{"const":"read"},"value":{"type":"integer"}},"required":["action","value"],"additionalProperties":false},{"type":"object","properties":{"action":{"const":"write"}},"required":["action"]}]}`,
			valid:   []string{`{"action":"read","value":1}`, `{"action":"write","value":"anything"}`, `{"action":"write","value":null}`},
			invalid: []string{`{"action":"read","value":"anything"}`, `{"action":"unknown"}`},
		},
		{
			name:    "additional property schema and exact integer bound",
			schema:  `{"oneOf":[{"type":"object","properties":{"action":{"const":"read"},"value":{"type":"integer","maximum":18446744073709551615}},"required":["action","value"],"additionalProperties":false},{"type":"object","properties":{"action":{"const":"write"}},"required":["action"],"additionalProperties":{"type":"boolean"}}]}`,
			valid:   []string{`{"action":"read","value":18446744073709551615}`, `{"action":"write","value":true}`},
			invalid: []string{`{"action":"read","value":18446744073709551616}`, `{"action":"write","value":"anything"}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projected, err := objectUnionInputSchema(tc.schema)
			require.NoError(t, err)
			var original, output map[string]jsontext.Value
			require.NoError(t, json.Unmarshal([]byte(tc.schema), &original))
			require.NoError(t, json.Unmarshal([]byte(projected), &output))
			assert.Equal(t, `"object"`, string(output["type"]))
			// Retained raw numeric tokens must not pass through float64.
			var originalBranches, outputBranches []map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(original["oneOf"], &originalBranches))
			require.NoError(t, json.Unmarshal(output["oneOf"], &outputBranches))
			assert.Equal(t, originalBranches, outputBranches)
			for _, input := range tc.valid {
				assert.NoError(t, sdkbridge.ValidateToolArguments([]byte(input), tc.schema), input)
				assert.NoError(t, sdkbridge.ValidateToolArguments([]byte(input), projected), input)
			}
			for _, input := range tc.invalid {
				require.Error(t, sdkbridge.ValidateToolArguments([]byte(input), tc.schema), input)
				assert.Error(t, sdkbridge.ValidateToolArguments([]byte(input), projected), input)
			}
		})
	}
}
