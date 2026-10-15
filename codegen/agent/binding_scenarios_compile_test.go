package codegen_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// These bindings cover shared method types and a service boundary. Compiling
// the complete generated module catches import, alias and adapter mismatches.
func TestGeneratedBindingScenariosCompile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design func()
	}{
		{"shared payload and result", testscenarios.AliasBoth()},
		{"shared payload", testscenarios.AliasPayloadOnly()},
		{"shared result", testscenarios.AliasResultOnly()},
		{"cross service", testscenarios.ServiceToolsetBindCross()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, tc.design)
			require.NotEmpty(t, files)
			dir := writeGeneratedModule(t, files)
			command := exec.CommandContext(t.Context(), "go", "test", "./...")
			command.Dir = dir
			command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
			output, err := command.CombinedOutput()
			require.NoError(t, err, "generated binding module: %s", output)
		})
	}
}
