package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unavailableTranscriptStore struct{ memory.Store }

type ingestionService struct {
	memory.Service
	runs    []memory.IngestRunInput
	deltas  []memory.IngestEventsInput
	entries []memory.PutEntryInput
	err     error
}

func TestRuntimeMemoryIngestionLoadsOnlyMissingEvents(t *testing.T) {
	ctx := t.Context()
	stored := memory.NewEvent(time.Unix(1, 0), memory.UserMessageData{Message: "stored"}, nil)
	explicit := memory.NewEvent(time.Unix(2, 0), memory.AssistantMessageData{Message: "explicit"}, nil)
	store := memoryinmem.New()
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", stored))
	service := &ingestionService{}
	runtime := &Runtime{Memory: store, MemoryService: service}
	scope := memory.Scope{Namespace: "tenant", UserID: "user", Visibility: memory.VisibilityUser}
	input := memory.IngestRunInput{AgentID: "agent", RunID: "run", SessionID: "session", Scope: scope, Labels: map[string]string{"key": "value"}}
	result, err := runtime.AddRunToMemory(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Skipped)
	require.Len(t, service.runs, 1)
	assert.Equal(t, []memory.Event{stored}, service.runs[0].Events)
	assert.Equal(t, scope, service.runs[0].Scope)
	assert.Equal(t, input.Labels, service.runs[0].Labels)
	assert.Nil(t, input.Events, "loading a snapshot must not mutate the caller's input")
	runtime.Memory = nil
	input.Events = []memory.Event{explicit}
	_, err = runtime.AddRunToMemory(ctx, input)
	require.NoError(t, err)
	require.Len(t, service.runs, 2)
	assert.Equal(t, input, service.runs[1], "explicit events must not require a transcript store")
}

func TestRuntimeMemoryWritesPreserveResultsAndErrors(t *testing.T) {
	event := memory.NewEvent(time.Unix(1, 0), memory.UserMessageData{Message: "remember"}, nil)
	for _, failure := range []error{nil, assert.AnError} {
		service := &ingestionService{err: failure}
		runtime := &Runtime{MemoryService: service}
		delta := memory.IngestEventsInput{AgentID: "agent", RunID: "run", StartOrdinal: 7, Events: []memory.Event{event}}
		result, err := runtime.AddEventsToMemory(t.Context(), delta)
		require.ErrorIs(t, err, failure)
		assert.Equal(t, 1, result.Skipped)
		assert.Equal(t, []memory.IngestEventsInput{delta}, service.deltas)
		direct := memory.PutEntryInput{Content: "remember", Scope: memory.Scope{Namespace: "tenant", Visibility: memory.VisibilityShared}}
		entry, err := runtime.PutMemoryEntry(t.Context(), direct)
		require.ErrorIs(t, err, failure)
		assert.Equal(t, "remember", entry.Content)
		assert.Equal(t, direct.Scope, entry.Scope)
		assert.Equal(t, []memory.PutEntryInput{direct}, service.entries)
		_, err = runtime.AddRunToMemory(t.Context(), memory.IngestRunInput{Events: []memory.Event{event}})
		require.ErrorIs(t, err, failure)
	}
}

func TestRuntimeMemoryIngestionFailsWithoutDependencies(t *testing.T) {
	runtime := &Runtime{}
	_, err := runtime.AddRunToMemory(t.Context(), memory.IngestRunInput{})
	require.ErrorContains(t, err, "memory service")
	_, err = runtime.AddEventsToMemory(t.Context(), memory.IngestEventsInput{})
	require.ErrorContains(t, err, "memory service")
	_, err = runtime.PutMemoryEntry(t.Context(), memory.PutEntryInput{})
	require.ErrorContains(t, err, "memory service")
	service := &ingestionService{}
	runtime.MemoryService = service
	_, err = runtime.AddRunToMemory(t.Context(), memory.IngestRunInput{})
	require.ErrorContains(t, err, "memory store")
	result, err := runtime.AddEventsToMemory(t.Context(), memory.IngestEventsInput{})
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Empty(t, service.deltas)
	runtime.Memory = unavailableTranscriptStore{}
	_, err = runtime.AddRunToMemory(t.Context(), memory.IngestRunInput{AgentID: "agent", RunID: "run"})
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, service.runs)
}

func (unavailableTranscriptStore) LoadRun(context.Context, string, string) (memory.Snapshot, error) {
	return memory.Snapshot{}, assert.AnError
}

func (s *ingestionService) IngestRun(_ context.Context, in memory.IngestRunInput) (memory.IngestResult, error) {
	s.runs = append(s.runs, in)
	return memory.IngestResult{Skipped: 2}, s.err
}

func (s *ingestionService) IngestEvents(_ context.Context, in memory.IngestEventsInput) (memory.IngestResult, error) {
	s.deltas = append(s.deltas, in)
	return memory.IngestResult{Skipped: 1}, s.err
}

func (s *ingestionService) PutEntry(_ context.Context, in memory.PutEntryInput) (memory.Entry, error) {
	s.entries = append(s.entries, in)
	return memory.Entry{ID: "entry", Content: in.Content, Scope: in.Scope}, s.err
}
