package tests

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
)

// Deeply nested user types with validations at each level should emit
// validators for every user type referenced by the payload and wire
// validation errors to ValidationError for retry hints.
func TestGolden_DeepNested_Validations(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.DeepNestedValidations())
	codecs := fileContent(t, files, "gen/alpha/toolsets/deep/codecs.go")
	if strings.Contains(codecs, "msg: err.Error()") {
		t.Fatal("generated validation errors retain private rejected values")
	}
	if !strings.Contains(codecs, `msg:    "value failed schema validation"`) {
		t.Fatal("generated validation errors do not use framework-authored safe text")
	}
	assertGoldenGo(t, "deep_nested_validations", "codecs.go.golden", codecs)
}
