package registry_test

import (
	"context"
	"encoding/json/v2"
	"flag"
	"os"
	"testing"
	"time"

	registrycli "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/cli/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryCLICommandsReachSelectedOperation(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.command, func(t *testing.T) {
			body, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			registryCLIArguments(t, "registry", tc.command, "--message", string(body))
			conn := registryTransportConnection(t, tc, func(_ context.Context, request any) (any, error) {
				assert.Equal(t, tc.payload, request)
				return tc.result, nil
			})
			endpoint, payload, err := registrycli.ParseEndpoint(conn)
			require.NoError(t, err)
			require.NotNil(t, endpoint)
			assert.Equal(t, tc.payload, payload)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := endpoint(ctx, payload)
			require.NoError(t, err)
			if tc.result != nil {
				assert.Equal(t, tc.result, result)
			}
		})
	}
}

func TestRegistryCLIRejectsUnknownCommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing operation", []string{"registry"}, "not enough arguments"},
		{"unknown service", []string{"missing", "search"}, "unknown service"},
		{"unknown operation", []string{"registry", "missing"}, "endpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registryCLIArguments(t, tc.args...)
			endpoint, payload, err := registrycli.ParseEndpoint(nil)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, endpoint)
			assert.Nil(t, payload)
		})
	}
}

// Generated CLI parsing uses process arguments and the default flag set.
// Keep these tests sequential and restore both globals before another test runs.
func registryCLIArguments(t *testing.T, args ...string) {
	t.Helper()
	previousArgs, previousFlags := os.Args, flag.CommandLine
	t.Cleanup(func() {
		os.Args = previousArgs
		flag.CommandLine = previousFlags
	})
	os.Args = append([]string{"registry-cli"}, args...)
	flag.CommandLine = flag.NewFlagSet("registry-cli", flag.ContinueOnError)
}
