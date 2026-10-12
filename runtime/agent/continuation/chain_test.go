package continuation

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chainPositionFault struct{ session.ContinuationStore }

func TestChainUsesOwnPredecessor(t *testing.T) {
	store := inmem.New()
	_, err := store.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	var prev *string
	for i, id := range []string{"42", "root-two", "later"} {
		require.NoError(t, store.UpsertRun(t.Context(), session.RunMeta{RunID: id, AgentID: "a", SessionID: "s", Status: session.RunStatusCompleted}))
		_, err = store.AdvanceContinuation(t.Context(), "s", prev, session.Continuation{ID: id, Prev: prev, Pos: i + 1, Checkpoint: new(runstore.Hash("checkpoint"))})
		require.NoError(t, err)
		prev = new(id)
	}
	chain := Chain{RunID: "current", SessionID: "s", Prev: new("root-two"), Pos: 3, Sessions: store, Continuations: store}
	resolved, err := chain.Resolve(t.Context(), "current", "r1")
	require.NoError(t, err)
	assert.Equal(t, "42", resolved.Run)
	resolved, err = chain.Resolve(t.Context(), "current", "42")
	require.NoError(t, err)
	assert.Equal(t, "42", resolved.Run)
	_, err = chain.Resolve(t.Context(), "current", "r3")
	require.ErrorContains(t, err, "recall_not_found")
	_, err = chain.Resolve(t.Context(), "child", "r1")
	require.ErrorContains(t, err, "recall_not_found")
}

func TestCarriedCitationCannotBecomeCurrentRun(t *testing.T) {
	chain := &Chain{RunID: "third", Pos: 3}
	entries := []index.MessageEntry{{ID: 1, Kind: index.Assistant, Origin: &index.Origin{Scope: "first", Run: "first", Pos: 1, ID: 8, Form: Full}}}
	resolver := chain.Citations(t.Context(), nil, entries, 1, nil)
	_, found, err := resolver.Resolve(checkpoint.OrderKey{Pos: 3, ID: 1})
	require.NoError(t, err)
	assert.False(t, found)
}

func TestChainRejectsForeignSessionAndPosition(t *testing.T) {
	for _, fault := range []string{"foreign_session", "position"} {
		t.Run(fault, func(t *testing.T) {
			store := inmem.New()
			for _, id := range []string{"s", "other"} {
				_, err := store.CreateSession(t.Context(), id, time.Now())
				require.NoError(t, err)
			}
			owner := "s"
			if fault == "foreign_session" {
				owner = "other"
			}
			require.NoError(t, store.UpsertRun(t.Context(), session.RunMeta{RunID: "previous", SessionID: owner, AgentID: "a", Status: session.RunStatusCompleted}))
			_, err := store.AdvanceContinuation(t.Context(), "s", nil, session.Continuation{ID: "previous", Pos: 1})
			require.NoError(t, err)
			var continuations session.ContinuationStore = store
			if fault == "position" {
				continuations = chainPositionFault{store}
			}
			chain := Chain{RunID: "current", SessionID: "s", Prev: new("previous"), Pos: 2, Sessions: store, Continuations: continuations}
			_, err = chain.Resolve(t.Context(), "current", "previous")
			require.ErrorContains(t, err, "recall_not_found")
		})
	}
}

func (s chainPositionFault) LoadContinuation(ctx context.Context, sessionID, id string) (session.Continuation, error) {
	body, err := s.ContinuationStore.LoadContinuation(ctx, sessionID, id)
	body.Pos++
	return body, err
}
