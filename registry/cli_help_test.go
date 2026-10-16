package registry_test

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"testing"

	registrycli "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/cli/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generated endpoint flag sets intentionally exit on --help. Isolate them
// in the current test executable so help cannot terminate the parent suite.
func TestRegistryCLIHelpDescribesEveryCommand(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	cases := registryTransportCases()
	commands := make([]string, 1, len(cases)+1)
	commands[0] = "service"
	for _, tc := range cases {
		commands = append(commands, tc.command)
	}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestRegistryCLIHelpProcess$")
			command.Env = append(os.Environ(), "LOOM_MCP_TEST_CLI_HELP="+name)
			// Let go test merge child-process counters into its own normal profile.
			if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
				command.Env = append(command.Env, "GOCOVERDIR="+directory.Value.String())
				command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
			}
			output, err := command.CombinedOutput()
			require.NoError(t, err, "help must exit successfully: %s", output)
			assert.Contains(t, string(output), "registry")
			if name == "service" {
				for _, tc := range cases {
					assert.Contains(t, string(output), tc.command+":")
				}
			} else {
				assert.Contains(t, string(output), "registry "+name)
				assert.Contains(t, string(output), "-message JSON")
				assert.Contains(t, string(output), "Example:")
			}
		})
	}
}

func TestRegistryCLIHelpProcess(t *testing.T) {
	name := os.Getenv("LOOM_MCP_TEST_CLI_HELP")
	if name == "" {
		return
	}
	args := []string{"registry", name, "--help"}
	if name == "service" {
		args = []string{"registry", "--help"}
	}
	registryCLIArguments(t, args...)
	_, _, err := registrycli.ParseEndpoint(nil)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	require.NoError(t, err)
	t.Fatal("endpoint help should exit before attempting a request")
}
