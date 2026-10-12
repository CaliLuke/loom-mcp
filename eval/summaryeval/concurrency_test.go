package summaryeval

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type concurrentClient struct {
	model.Client
	mu          sync.Mutex
	active, max int
	ready       chan struct{}
}

func TestConcurrency(t *testing.T) {
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("same")), agenttest.Respond(agenttest.Text("same")))
	require.NoError(t, err)
	client := &concurrentClient{Client: script.Client(), ready: make(chan struct{})}
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return map[string]Model{"summary": {Client: client, Limits: &model.ContextLimits{ContextWindow: 200000}}}, nil
	}}
	v, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	result, err := Run(t.Context(), cfg, Options{Dir: t.TempDir(), Cuts: Cuts(recordedFixture(t)), Variants: []Variant{v}, Repeat: 2, Concurrency: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, client.max)
	assert.Equal(t, 1, result.Lines[0].Repeat)
	assert.Equal(t, 2, result.Lines[1].Repeat)
}

func (c *concurrentClient) Complete(ctx context.Context, r *model.Request) (*model.Response, error) {
	c.mu.Lock()
	c.active++
	c.max = max(c.max, c.active)
	if c.active == 2 {
		close(c.ready)
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()
	select {
	case <-c.ready:
	case <-time.After(time.Second):
		return nil, fmt.Errorf("concurrency did not start a second call")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.Client.Complete(ctx, r)
}
