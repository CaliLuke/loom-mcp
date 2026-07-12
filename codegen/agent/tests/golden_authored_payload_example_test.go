package tests

import (
	"encoding/json"
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	"github.com/stretchr/testify/require"
)

func TestAuthoredPayloadExamplePreservedInToolSpecs(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.AuthoredPayloadExample())
	specsSrc := fileContent(t, files, "gen/alpha/toolsets/helpers/specs.go")

	require.Contains(t, specsSrc, `ExampleJSON: []byte("{\"limit\":7,\"query\":\"battery alarms\"}")`)
}

func TestInjectedPayloadFieldsHiddenFromAdvertisedToolSpecs(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.InjectedPayloadField())
	specsSrc := fileContent(t, files, "gen/assistant/toolsets/lookup/specs.go")
	injectSrc := fileContent(t, files, "gen/assistant/toolsets/lookup/inject.go")
	providerSrc := fileContent(t, files, "gen/assistant/toolsets/lookup/provider.go")

	require.NotContains(t, toolSpecLiteral(t, specsSrc, "Schema"), "session_id")
	require.NotContains(t, toolSpecLiteral(t, specsSrc, "ExampleJSON"), "session_id")
	require.NotContains(t, toolSpecLiteral(t, specsSrc, "ExampleInput"), "session_id")
	require.Contains(t, injectSrc, "sessionIDValue := meta.SessionID")
	require.Contains(t, injectSrc, "payload.SessionID = sessionIDValue")
	require.Contains(t, providerSrc, "payload.SessionID = meta.SessionID")
	require.Contains(t, providerSrc, "methodIn.SessionID = msg.Meta.SessionID")
	require.Contains(t, providerSrc, "methodOut, err := p.svc.Lookup(ctx, methodIn)")
}

func TestBadUnionPayloadExampleReportsToolAndPath(t *testing.T) {
	genpkg, roots := testhelpers.RunDesign(t, testscenarios.BadUnionPayloadExample())

	var recovered any
	var genErr error
	func() {
		defer func() {
			recovered = recover()
		}()
		_, genErr = agentcodegen.Generate(genpkg, roots, nil)
	}()

	err := genErr
	if recovered != nil {
		var ok bool
		err, ok = recovered.(error)
		require.Truef(t, ok, "expected generator panic with error, got %T", recovered)
	}
	require.Error(t, err)
	require.ErrorContains(t, err, `agent "scribe"`)
	require.ErrorContains(t, err, "helpers.bad_union_example")
	require.ErrorContains(t, err, "payload.value")
	require.ErrorContains(t, err, "does not match any variant")
}

func toolSpecLiteral(t *testing.T, specsSrc string, field string) string {
	t.Helper()
	start := `Payload: tools.TypeSpec{`
	payloadStart := requireSubstringIndex(t, specsSrc, start) + len(start)
	fieldStart := requireSubstringIndex(t, specsSrc[payloadStart:], field+": ") + payloadStart + len(field+": ")
	switch field {
	case "Schema", "ExampleJSON":
		byteStart := requireSubstringIndex(t, specsSrc[fieldStart:], "[]byte(") + fieldStart + len("[]byte(")
		byteEnd := requireSubstringIndex(t, specsSrc[byteStart:], "),") + byteStart
		var decoded string
		require.NoError(t, json.Unmarshal([]byte(specsSrc[byteStart:byteEnd]), &decoded))
		return decoded
	case "ExampleInput":
		inputEnd := requireSubstringIndex(t, specsSrc[fieldStart:], ", Codec:") + fieldStart
		return specsSrc[fieldStart:inputEnd]
	default:
		t.Fatalf("unsupported tool spec field %q", field)
		return ""
	}
}

func requireSubstringIndex(t *testing.T, s string, substr string) int {
	t.Helper()
	for i := range s {
		if len(s[i:]) >= len(substr) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	require.Failf(t, "substring not found", "expected %q in:\n%s", substr, s)
	return -1
}
