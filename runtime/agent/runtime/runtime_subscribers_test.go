package runtime

import (
	"context"
	"sync"
	"testing"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seamSessionStore records UpsertRun calls and defers all other methods to a
// zero-value store; only UpsertRun is exercised by the seam test.
type seamSessionStore struct {
	session.Store
	mu      sync.Mutex
	upserts []session.RunMeta
}

func (s *seamSessionStore) UpsertRun(_ context.Context, r session.RunMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, r)
	return nil
}

type seamMemoryStore struct {
	memory.Store
	mu      sync.Mutex
	appends []memory.Event
}

func (m *seamMemoryStore) AppendEvents(_ context.Context, _ string, _ string, events ...memory.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appends = append(m.appends, events...)
	return nil
}

// TestNewFromOptionsInstallsSessionAndMemorySubscribers proves that
// newFromOptions + installRuntimeSubscribers wires session and memory
// subscribers onto the bus returned on the runtime. Publishing a run-lifecycle
// event must land in the session store; publishing a planner-observable event
// must land in the memory store.
func TestNewFromOptionsInstallsSessionAndMemorySubscribers(t *testing.T) {
	bus := hooks.NewBus()
	sessionStore := &seamSessionStore{}
	memoryStore := &seamMemoryStore{}

	rt := newFromOptions(Options{
		Hooks:        bus,
		SessionStore: sessionStore,
		MemoryStore:  memoryStore,
	})
	require.NotNil(t, rt)

	ctx := context.Background()

	runStarted := hooks.NewRunStartedEvent(
		"run-1",
		agent.Ident("agent-1"),
		run.Context{RunID: "run-1", SessionID: "session-1"},
		nil,
	)
	require.NoError(t, bus.Publish(ctx, runStarted))

	assistantMsg := hooks.NewAssistantMessageEvent(
		"run-1",
		agent.Ident("agent-1"),
		"session-1",
		"hello",
		nil,
	)
	require.NoError(t, bus.Publish(ctx, assistantMsg))

	sessionStore.mu.Lock()
	defer sessionStore.mu.Unlock()
	memoryStore.mu.Lock()
	defer memoryStore.mu.Unlock()

	assert.Len(t, sessionStore.upserts, 1, "session subscriber should record RunStarted via the bus wired by newFromOptions")
	if len(sessionStore.upserts) == 1 {
		assert.Equal(t, "run-1", sessionStore.upserts[0].RunID)
		assert.Equal(t, session.RunStatusRunning, sessionStore.upserts[0].Status)
	}
	assert.Len(t, memoryStore.appends, 1, "memory subscriber should record AssistantMessage via the bus wired by newFromOptions")
}
