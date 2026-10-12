package mongo

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/require"
)

func TestStoreContinuationCapability(t *testing.T) {
	store, err := NewStore(&inMemorySessionClient{Store: sessioninmem.New()})
	require.NoError(t, err)
	cs, ok := any(store).(session.ContinuationStore)
	require.True(t, ok)
	_, err = store.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	outcome, err := cs.AdvanceContinuation(t.Context(), "s", nil, session.Continuation{ID: "r", Pos: 1})
	require.NoError(t, err)
	require.Equal(t, session.Published, outcome)
	head, err := cs.LoadContinuationHead(t.Context(), "s")
	require.NoError(t, err)
	require.Equal(t, "r", head.ID)
}
