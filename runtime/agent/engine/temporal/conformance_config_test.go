package temporal_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

func TestConformanceWorkerConfiguration(t *testing.T) {
	// A lazy client performs no server handshake. Registration constructs the
	// SDK worker, but polling starts only after SealRegistration, which is omitted.
	c, err := client.NewLazyClient(client.Options{
		HostPort:     "127.0.0.1:1",
		Interceptors: []interceptor.ClientInterceptor{&conformanceStartInterceptor{}},
	})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	fixture := temporalConformanceFixture(t, c)
	require.NoError(t, fixture.Engine.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{
		Name: "offline-conformance", TaskQueue: fixture.TaskQueue,
		Handler: func(_ engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			return &api.RunOutput{}, nil
		},
	}))
}
