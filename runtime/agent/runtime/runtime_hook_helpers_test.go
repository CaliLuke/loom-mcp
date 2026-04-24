package runtime

import (
	"context"
	"errors"
	"testing"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBestEffortSubscriberErrorDoesNotPropagate proves that a subscriber
// registered via registerSubscriber with SubscriberBestEffort cannot fail the
// hook activity: its HandleEvent error is logged and recorded on the tracer
// but never surfaces back through Bus.Publish, nor through publishHookBusEvent.
func TestBestEffortSubscriberErrorDoesNotPropagate(t *testing.T) {
	bus := hooks.NewBus()
	rt := &Runtime{
		Bus:     bus,
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}

	sentinel := errors.New("best-effort boom")
	best := hooks.SubscriberFunc(func(context.Context, hooks.Event) error {
		return sentinel
	})
	_, err := rt.registerSubscriber(bus, best, SubscriberBestEffort)
	require.NoError(t, err)

	evt := hooks.NewRunStartedEvent("run-1", agent.Ident("svc.agent"), run.Context{RunID: "run-1"}, nil)

	assert.NoError(t, bus.Publish(context.Background(), evt), "best-effort subscriber error must not surface through Bus.Publish")
	assert.NoError(t, rt.publishHookBusEvent(context.Background(), evt.Type(), evt), "publishHookBusEvent must swallow best-effort subscriber errors")
}

// TestCriticalSubscriberErrorFailsRunFast proves that a subscriber registered
// via registerSubscriber with SubscriberCritical propagates its error through
// publishHookBusEvent so the hook activity (and therefore the run) fails fast.
func TestCriticalSubscriberErrorFailsRunFast(t *testing.T) {
	bus := hooks.NewBus()
	rt := &Runtime{
		Bus:     bus,
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}

	sentinel := errors.New("critical boom")
	critical := hooks.SubscriberFunc(func(context.Context, hooks.Event) error {
		return sentinel
	})
	_, err := rt.registerSubscriber(bus, critical, SubscriberCritical)
	require.NoError(t, err)

	evt := hooks.NewRunStartedEvent("run-1", agent.Ident("svc.agent"), run.Context{RunID: "run-1"}, nil)
	err = rt.publishHookBusEvent(context.Background(), evt.Type(), evt)
	require.Error(t, err, "critical subscriber error must propagate out of publishHookBusEvent")
	assert.ErrorIs(t, err, sentinel)
}
