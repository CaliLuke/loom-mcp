package codex

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

func TestRequestPreservesIntegerSchemaBounds(t *testing.T) {
	const schema = `{"type":"object","properties":{"signed":{"type":"integer","minimum":-9223372036854775808,"maximum":9223372036854775807},"unsigned":{"type":"integer","minimum":0,"maximum":18446744073709551615}}}`
	for _, input := range []any{rawjson.Message(schema), jsontext.Value(schema)} {
		for _, lite := range []bool{false, true} {
			client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", ResponsesLite: lite})
			require.NoError(t, err)
			request := testRequest()
			request.Tools = []*model.ToolDefinition{{Name: "numbers", InputSchema: input}}
			built, err := client.buildRequest(request)
			require.NoError(t, err)
			require.NoError(t, built.prepare(TransportAuto))
			for _, body := range [][]byte{built.sseBody, built.wsBody} {
				assert.Contains(t, string(body), `"strict":false`)
				assert.Contains(t, string(body), `"minimum":-9223372036854775808`)
				assert.Contains(t, string(body), `"maximum":9223372036854775807`)
				assert.Contains(t, string(body), `"maximum":18446744073709551615`)
				assert.True(t, jsontext.Value(body).IsValid())
				var envelope map[string]jsontext.Value
				require.NoError(t, json.Unmarshal(body, &envelope))
			}
		}
	}
}
