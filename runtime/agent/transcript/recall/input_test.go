package recall

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInput(t *testing.T) {
	cases := []struct {
		name, payload string
		kind          Kind
		valid         bool
	}{
		{"read result", `{"read":{"id":"toolu_a","part":"result","offset":0}}`, Read, true},
		{"read arguments", `{"read":{"id":"r1:3","run":"r1","part":"arguments","offset":4}}`, Read, true},
		{"summary", `{"read":{"id":"r1:ck-12345678","part":"result","offset":0}}`, Read, true},
		{"handle", `{"list":{"handle":"rc-12345678","cursor":"opaque"}}`, List, true},
		{"range", `{"list":{"from":1,"through":9,"run":"r2"}}`, List, true},
		{"literal", `{"find":{"text":"世界 Case","cursor":"opaque"}}`, Find, true},
		{"no kind", `{}`, "", false},
		{"two kinds", `{"read":{},"list":{}}`, "", false},
		{"unknown kind", `{"scan":{}}`, "", false},
		{"null", `null`, "", false},
		{"array", `[]`, "", false},
		{"trailing", `{} {}`, "", false},
		{"duplicate", `{"read":{},"read":{}}`, "", false},
		{"read null", `{"read":null}`, Read, false},
		{"missing id", `{"read":{"part":"result","offset":0}}`, Read, false},
		{"empty id", `{"read":{"id":"","part":"result","offset":0}}`, Read, false},
		{"missing part", `{"read":{"id":"3","offset":0}}`, Read, false},
		{"invalid part", `{"read":{"id":"3","part":"server","offset":0}}`, Read, false},
		{"missing offset", `{"read":{"id":"3","part":"result"}}`, Read, false},
		{"negative offset", `{"read":{"id":"3","part":"result","offset":-1}}`, Read, false},
		{"fraction offset", `{"read":{"id":"3","part":"result","offset":0.5}}`, Read, false},
		{"null offset", `{"read":{"id":"3","part":"result","offset":null}}`, Read, false},
		{"extra read field", `{"read":{"id":"3","part":"result","offset":0,"scope":"other"}}`, Read, false},
		{"mixed listing", `{"list":{"handle":"rc-12345678","from":1,"through":3}}`, List, false},
		{"partial range", `{"list":{"from":1}}`, List, false},
		{"backwards range", `{"list":{"from":4,"through":3}}`, List, false},
		{"zero range", `{"list":{"from":0,"through":3}}`, List, false},
		{"handle run", `{"list":{"handle":"rc-12345678","run":"r2"}}`, List, false},
		{"empty search", `{"find":{"text":""}}`, Find, false},
		{"null search", `{"find":{"text":null}}`, Find, false},
		{"null run", `{"read":{"id":"3","part":"result","offset":0,"run":null}}`, Read, false},
		{"case", `{"Read":{}}`, "", false},
		{"empty handle", `{"list":{"handle":""}}`, List, false},
		{"null cursor", `{"list":{"handle":"rc-12345678","cursor":null}}`, List, false},
		{"offset overflow", `{"read":{"id":"3","part":"result","offset":999999999999999999999999999}}`, Read, false},
		{"null range", `{"list":{"from":null,"through":3}}`, List, false},
		{"extra find field", `{"find":{"text":"fact","run":"r2"}}`, Find, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			in, err := ParseInput([]byte(tt.payload))
			assert.Equal(t, tt.kind, in.Kind)
			if tt.valid {
				require.NoError(t, err)
				return
			}
			var typed *Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, CodeInvalidInput, typed.Code)
		})
	}
}

func TestSpecAndSchema(t *testing.T) {
	spec := Spec()
	assert.Equal(t, tools.RecallContext, tools.Ident("runtime.recall_context"))
	assert.Equal(t, tools.RecallContext, spec.Name)
	assert.Equal(t, Description, spec.Description)
	for _, word := range []string{"placeholder", "pin", "index", "receipt", "handle", "checkpoint", "offset", "next_offset", "cursor", "case-sensitive", "discovery", "read", "compaction", "arguments", "512"} {
		assert.Contains(t, Description, word)
	}
	var schema any
	require.NoError(t, json.Unmarshal(spec.Payload.Schema, &schema))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("recall.json", schema))
	compiled, err := compiler.Compile("recall.json")
	require.NoError(t, err)
	for _, payload := range []string{`{"read":{"id":"3","part":"result","offset":0}}`, `{"list":{"handle":"rc-12345678"}}`, `{"list":{"from":1,"through":3}}`, `{"find":{"text":"fact"}}`} {
		var value any
		require.NoError(t, json.Unmarshal([]byte(payload), &value))
		assert.NoError(t, compiled.Validate(value))
	}
	for _, payload := range []string{`{}`, `{"read":{},"find":{}}`, `{"read":{"id":"3","part":"server","offset":0}}`, `{"list":{"handle":"rc-12345678","from":1,"through":3}}`, `{"find":{"text":""}}`} {
		var value any
		require.NoError(t, json.Unmarshal([]byte(payload), &value))
		require.Error(t, compiled.Validate(value))
	}
	decoded, err := spec.Payload.Codec.FromJSON([]byte(`{"find":{"text":"fact"}}`))
	require.NoError(t, err)
	assert.Equal(t, Find, decoded.(Input).Kind)
	encoded, err := spec.Payload.Codec.ToJSON(decoded)
	require.NoError(t, err)
	assert.JSONEq(t, `{"find":{"text":"fact"}}`, string(encoded))
	// Schema bytes are owned: mutation cannot corrupt another caller's spec.
	spec.Payload.Schema[0] = 'x'
	assert.Equal(t, byte('{'), Spec().Payload.Schema[0])
}

func TestValidateToolName(t *testing.T) {
	for _, tt := range []struct {
		id       tools.Ident
		provider string
		rejected bool
	}{
		{"svc.search", "svc_search", false},
		{"svc.recall_context_extra", "safe", true},
		{"svc.safe", "prefix_recall_context_suffix", true},
		{tools.RecallContext, "runtime_recall_context", true},
	} {
		err := ValidateToolName(tt.id, tt.provider)
		assert.Equal(t, tt.rejected, err != nil)
	}
}

func TestErrorCodes(t *testing.T) {
	for _, code := range []Code{CodeBudgetExhausted, CodeNotFound, CodeUnauthorized, CodeUnavailable, CodeInvalidInput, CodeUnsupportedPart} {
		data, err := json.Marshal(&Error{Code: code})
		require.NoError(t, err)
		assert.JSONEq(t, `{"code":"`+string(code)+`"}`, string(data))
		assert.True(t, strings.HasPrefix((&Error{Code: code}).Error(), "recall_"))
	}
}
