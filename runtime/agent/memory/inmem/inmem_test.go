package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/stretchr/testify/require"
)

type typedStructuredPayload struct {
	Items []string
}

func TestStoreAppendAndLoad(t *testing.T) {
	store := New()
	ctx := context.Background()
	event := memory.Event{Type: memory.EventToolCall, Timestamp: time.Now(), Data: map[string]any{"tool": "foo"}}
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", event))
	snap, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	require.Len(t, snap.Events, 1)
	require.Equal(t, memory.EventToolCall, snap.Events[0].Type)
}

func TestStoreIsolation(t *testing.T) {
	store := New()
	ctx := context.Background()
	first := memory.Event{Type: memory.EventToolCall}
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", first))
	snap, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	snap.Events[0].Type = memory.EventToolResult
	snap2, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	require.Equal(t, memory.EventToolCall, snap2.Events[0].Type, "store mutated by caller")
}

func TestStoreDefensivelyCopiesEventDataAndLabels(t *testing.T) {
	store := New()
	ctx := context.Background()
	event := memory.Event{
		Type: memory.EventToolCall,
		Data: map[string]any{
			"message":    "original",
			"nested":     map[string]any{"value": "original"},
			"items":      []any{map[string]any{"value": "original"}},
			"bytes":      []byte{1, 2, 3},
			"retry_hint": &memory.RetryHintData{ExampleInput: map[string]any{"value": "original"}},
		},
		Labels: map[string]string{"tenant": "original"},
	}
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", event))

	event.Data.(map[string]any)["message"] = "mutated"
	event.Data.(map[string]any)["nested"].(map[string]any)["value"] = "mutated"
	event.Data.(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "mutated"
	event.Data.(map[string]any)["bytes"].([]byte)[0] = 9
	event.Labels["tenant"] = "mutated"
	event.Data.(map[string]any)["retry_hint"].(*memory.RetryHintData).ExampleInput["value"] = "mutated"

	first, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	require.Equal(t, "original", first.Events[0].Data.(map[string]any)["message"])
	require.Equal(t, "original", first.Events[0].Data.(map[string]any)["nested"].(map[string]any)["value"])
	require.Equal(t, "original", first.Events[0].Data.(map[string]any)["items"].([]any)[0].(map[string]any)["value"])
	require.Equal(t, byte(1), first.Events[0].Data.(map[string]any)["bytes"].([]byte)[0])
	require.Equal(t, "original", first.Events[0].Labels["tenant"])
	require.Equal(t, "original", first.Events[0].Data.(map[string]any)["retry_hint"].(*memory.RetryHintData).ExampleInput["value"])

	first.Events[0].Data.(map[string]any)["message"] = "snapshot mutation"
	first.Events[0].Data.(map[string]any)["nested"].(map[string]any)["value"] = "snapshot mutation"
	first.Events[0].Data.(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "snapshot mutation"
	first.Events[0].Data.(map[string]any)["bytes"].([]byte)[0] = 8
	first.Events[0].Labels["tenant"] = "snapshot mutation"
	first.Events[0].Data.(map[string]any)["retry_hint"].(*memory.RetryHintData).ExampleInput["value"] = "snapshot mutation"

	second, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	require.Equal(t, "original", second.Events[0].Data.(map[string]any)["message"])
	require.Equal(t, "original", second.Events[0].Data.(map[string]any)["nested"].(map[string]any)["value"])
	require.Equal(t, "original", second.Events[0].Data.(map[string]any)["items"].([]any)[0].(map[string]any)["value"])
	require.Equal(t, byte(1), second.Events[0].Data.(map[string]any)["bytes"].([]byte)[0])
	require.Equal(t, "original", second.Events[0].Labels["tenant"])
	require.Equal(t, "original", second.Events[0].Data.(map[string]any)["retry_hint"].(*memory.RetryHintData).ExampleInput["value"])
}

func TestStoreDefensivelyCopiesTypedStructuredData(t *testing.T) {
	store := New()
	ctx := context.Background()
	event := memory.NewEvent(time.Unix(1, 0), memory.AssistantMessageData{
		Structured: &typedStructuredPayload{Items: []string{"original"}},
	}, nil)
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", event))

	event.Data.(map[string]any)["structured"].(*typedStructuredPayload).Items[0] = "caller mutation"
	first, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	firstPayload := first.Events[0].Data.(map[string]any)["structured"].(*typedStructuredPayload)
	require.Equal(t, "original", firstPayload.Items[0])

	firstPayload.Items[0] = "snapshot mutation"
	second, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	secondPayload := second.Events[0].Data.(map[string]any)["structured"].(*typedStructuredPayload)
	require.Equal(t, "original", secondPayload.Items[0])
}

func TestStorePreservesEmptyEventData(t *testing.T) {
	store := New()
	ctx := context.Background()
	event := memory.NewEvent(time.Unix(1, 0), memory.UserMessageData{}, nil)
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", event))

	snapshot, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	require.NotNil(t, snapshot.Events[0].Data)
	_, err = memory.DecodeUserMessageData(snapshot.Events[0])
	require.NoError(t, err)
}

func TestStoreDefensivelyCopiesEmptyArtifactMetadata(t *testing.T) {
	store := New()
	ctx := context.Background()
	event := memory.Event{
		Type: memory.EventToolResult,
		Data: map[string]any{
			"artifacts": []artifact.Ref{{Metadata: map[string]string{}}},
		},
	}
	require.NoError(t, store.AppendEvents(ctx, "agent", "run", event))

	event.Data.(map[string]any)["artifacts"].([]artifact.Ref)[0].Metadata["caller"] = "mutation"
	first, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	firstMetadata := first.Events[0].Data.(map[string]any)["artifacts"].([]artifact.Ref)[0].Metadata
	require.NotNil(t, firstMetadata)
	require.Empty(t, firstMetadata)

	firstMetadata["snapshot"] = "mutation"
	second, err := store.LoadRun(ctx, "agent", "run")
	require.NoError(t, err)
	secondMetadata := second.Events[0].Data.(map[string]any)["artifacts"].([]artifact.Ref)[0].Metadata
	require.NotNil(t, secondMetadata)
	require.Empty(t, secondMetadata)
}
