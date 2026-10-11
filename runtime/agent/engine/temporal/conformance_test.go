package temporal_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
)

// conformanceStartInterceptor bounds recovery of workflow tasks taken by a
// stopped conformance worker without changing production engine start options.
type conformanceStartInterceptor struct {
	interceptor.ClientInterceptorBase
}

type conformanceStartOutbound struct {
	interceptor.ClientOutboundInterceptorBase
}

const (
	conformanceTemporalCLIVersion = "v1.6.1"
	conformanceTaskTimeout        = time.Second
)

// TestConformance uses the same real dev-server hosting as agent_features. The
// SDK workflow test environment cannot exercise Engine client/status operations
// or persistence across replacement workers.
func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("server-backed engine conformance; run make test-engine-conformance")
	}
	started := time.Now()
	startupCtx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	server, err := testsuite.StartDevServer(startupCtx, testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: conformanceTemporalCLIVersion},
		ClientOptions: &client.Options{
			DataConverter: temporalengine.NewAgentDataConverter(nil),
			Logger:        temporallog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			Interceptors:  []interceptor.ClientInterceptor{&conformanceStartInterceptor{}},
		},
		LogLevel: "error",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		server.Client().Close()
		require.NoError(t, server.Stop())
		t.Logf("Temporal conformance including server lifecycle: %s", time.Since(started))
	})
	enginetest.RunConformance(t, enginetest.Factory{
		Durable: true, Retries: true,
		KnownGaps: map[string]string{
			"id_conflict":  "E0 follow-up: Temporal StartWorkflow returns the existing handle for an active ID because WorkflowExecutionErrorWhenAlreadyStarted is not set",
			"await_signal": "E0 follow-up: Await requires a side-effect-free predicate, but Receiver offers only consuming ReceiveAsync and WorkflowContext has no signal handler/coroutine",
		},
		New: func(t *testing.T) enginetest.Fixture {
			return temporalConformanceFixture(t, server.Client())
		},
	})
}

func (*conformanceStartInterceptor) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	return &conformanceStartOutbound{ClientOutboundInterceptorBase: interceptor.ClientOutboundInterceptorBase{Next: next}}
}

func (c *conformanceStartOutbound) ExecuteWorkflow(ctx context.Context, input *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
	input.Options.WorkflowTaskTimeout = conformanceTaskTimeout
	return c.Next.ExecuteWorkflow(ctx, input)
}

func temporalConformanceFixture(t *testing.T, c client.Client) enginetest.Fixture {
	t.Helper()
	queue := "engine-conformance-" + uuid.NewString()
	newEngine := func() *temporalengine.Engine {
		e, err := temporalengine.NewWorker(temporalengine.Options{
			Client:          c,
			WorkerOptions:   temporalengine.WorkerOptions{TaskQueue: queue, Options: worker.Options{DisableEagerActivities: true, StickyScheduleToStartTimeout: conformanceTaskTimeout}},
			Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, e.Close())
		})
		return e
	}
	current := newEngine()
	return enginetest.Fixture{
		Engine: current, TaskQueue: queue, ActivityQueue: queue + "-activities",
		Restart: func(t *testing.T, register func(engine.Engine)) engine.Engine {
			require.NoError(t, current.Close())
			current = newEngine()
			register(current)
			require.NoError(t, current.SealRegistration(t.Context()))
			return current
		},
		VerifyActivityOptions: func(t *testing.T, id, name string, want []engine.ActivityOptions) error {
			return verifyScheduledActivityOptions(t, c, id, name, want)
		},
	}
}
