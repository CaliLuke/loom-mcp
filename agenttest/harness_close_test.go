package agenttest

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseReportsResponderOnce(t *testing.T) {
	bus := hooks.NewBus()
	subscription, err := bus.Register(hooks.SubscriberFunc(func(context.Context, hooks.Event) error { return nil }))
	require.NoError(t, err)
	done := make(chan struct{})
	close(done)
	workerDone := make(chan struct{})
	defer close(workerDone)
	h := &Harness{runs: map[string]*harnessRun{"run": {
		done:   done,
		worker: &awaitWorker{done: workerDone, cancel: func() {}, subscription: subscription, open: true},
	}}}
	err = h.Close()
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "responder still alive"), err)
	assert.Contains(t, err.Error(), "run run")
}
