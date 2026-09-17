package assistantapi

import (
	"testing"

	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedIntegerSchemaMatchesCodec(t *testing.T) {
	spec := projected.SpecProjectedLookupTool
	contract, err := model.NewRequestContract(&model.Request{Tools: []*model.ToolDefinition{{
		Name: string(spec.Name), InputSchema: rawjson.Message(spec.Payload.Schema),
	}}})
	require.NoError(t, err)
	for _, tt := range []struct {
		name    string
		payload string
		valid   bool
	}{
		{"limits", `{"query":"test","count":9223372036854775807,"signed":-9223372036854775808,"unsigned":18446744073709551615}`, true},
		{"int overflow", `{"query":"test","count":9223372036854775808}`, false},
		{"int64 underflow", `{"query":"test","signed":-9223372036854775809}`, false},
		{"uint64 overflow", `{"query":"test","unsigned":18446744073709551616}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, codecErr := spec.Payload.Codec.FromJSON([]byte(tt.payload))
			_, contractErr := contract.ValidateResponse(&model.Response{StopReason: "tool_use", ToolCalls: []model.ToolCall{{
				ID: "number", Name: spec.Name, Payload: rawjson.Message(tt.payload),
			}}})
			if tt.valid {
				assert.NoError(t, codecErr)
				assert.NoError(t, contractErr)
			} else {
				assert.Error(t, codecErr)
				var validationErr *model.OutputValidationError
				require.ErrorAs(t, contractErr, &validationErr)
				assert.Equal(t, model.OutputValidationToolArguments, validationErr.Kind())
			}
		})
	}
}
