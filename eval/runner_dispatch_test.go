package eval

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunAttemptCanceledAtDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var built int
	factory := func(context.Context, AttemptInfo, ...agenttest.Option) (*agenttest.Harness, error) {
		built++
		return nil, errors.New("unexpected factory call")
	}
	key := AttemptKey{Variant: "default", Scenario: "scenario", Item: "item", Repeat: 1}
	// The job has already been dispatched: cancellation must still prevent entry
	// into the factory, independently of which select case won in collect.
	a := runAttempt(ctx, factory, &harnessOwners{seen: make(map[*agenttest.Harness]bool)}, runnerJob{index: 3, attempt: plannedAttempt{key: key}})
	assert.Zero(t, built)
	assert.Equal(t, key, a.AttemptKey)
	assert.Equal(t, 3, a.Index)
	assert.Equal(t, StopCanceled, a.NotRun)
	assert.Equal(t, VerdictNotRun, a.Verdict())
	assert.Nil(t, a.Outcome)
	require.NoError(t, a.HarnessErr)
	assert.True(t, a.Started.IsZero())
	assert.Zero(t, a.Duration)
}
