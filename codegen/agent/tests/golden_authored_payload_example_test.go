package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestAuthoredPayloadExamplePreservedInToolSpecs(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.AuthoredPayloadExample())
	specsSrc := fileContent(t, files, "gen/alpha/toolsets/helpers/specs.go")

	require.Contains(t, specsSrc, `ExampleJSON: []byte("{\"limit\":7,\"query\":\"battery alarms\"}")`)
}
