package inmem_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationPublication(t *testing.T) {
	s := inmem.New()
	_, err := s.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	cs, ok := any(s).(session.ContinuationStore)
	require.True(t, ok)
	first := session.Continuation{ID: "r1", Pos: 1}
	outcome, err := cs.AdvanceContinuation(t.Context(), "s", nil, first)
	require.NoError(t, err)
	assert.Equal(t, session.Published, outcome)
	next := session.Continuation{ID: "r2", Prev: new("r1"), Pos: 2}
	outcome, err = cs.AdvanceContinuation(t.Context(), "s", new("r1"), next)
	require.NoError(t, err)
	assert.Equal(t, session.Published, outcome)
	outcome, err = cs.AdvanceContinuation(t.Context(), "s", nil, first)
	require.NoError(t, err)
	assert.Equal(t, session.AlreadyPublished, outcome)
	first.Pos = 3
	outcome, err = cs.AdvanceContinuation(t.Context(), "s", nil, first)
	require.NoError(t, err)
	assert.Equal(t, session.Conflict, outcome)
	outcome, err = cs.AdvanceContinuation(t.Context(), "s", nil, session.Continuation{ID: "late", Pos: 1})
	require.NoError(t, err)
	assert.Equal(t, session.Superseded, outcome)
	head, err := cs.LoadContinuationHead(t.Context(), "s")
	require.NoError(t, err)
	require.NotNil(t, head)
	assert.Equal(t, "r2", head.ID)
	*head.Prev = "mutated"
	stored, err := cs.LoadContinuation(t.Context(), "s", "r2")
	require.NoError(t, err)
	assert.Equal(t, "r1", *stored.Prev)
	_, err = cs.LoadContinuation(t.Context(), "other", "r1")
	assert.ErrorIs(t, err, session.ErrContinuationNotFound)
}

func TestContinuationOneSuccessor(t *testing.T) {
	s := inmem.New()
	_, err := s.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	cs := any(s).(session.ContinuationStore)
	var wg sync.WaitGroup
	outcomes := make(chan session.AdvanceOutcome, 32)
	for i := range 32 {
		wg.Go(func() {
			outcome, err := cs.AdvanceContinuation(t.Context(), "s", nil, session.Continuation{ID: fmt.Sprintf("r%d", i), Pos: 1})
			assert.NoError(t, err)
			outcomes <- outcome
		})
	}
	wg.Wait()
	close(outcomes)
	published := 0
	for outcome := range outcomes {
		if outcome == session.Published {
			published++
		} else {
			assert.Equal(t, session.Superseded, outcome)
		}
	}
	assert.Equal(t, 1, published)
}
