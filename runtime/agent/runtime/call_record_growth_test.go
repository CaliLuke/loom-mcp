package runtime

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callByteStore struct {
	runstore.Store
	total int
}

func TestCallRecordGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("1000 model-call steps run in test-payload-growth")
	}
	for _, mode := range []string{"off", "real_compaction"} {
		t.Run(mode, func(t *testing.T) {
			store := &callByteStore{Store: inmem.New()}
			var view []*model.Message
			rt := New(WithRunStore(store.Store))
			var state compaction.State
			var ref index.ViewRef
			var allEntries []index.MessageEntry
			compacted := 0
			var head, chain runstore.Hash
			if mode == "real_compaction" {
				view, allEntries, head, chain = seedGrowthView(t, store.Store)
			}
			points := make([]float64, 1000)
			for step := 1; step <= 1000; step++ {
				additions := []*model.Message{
					{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: fmt.Sprintf("question %04d", step)}}},
					{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: fmt.Sprintf("answer %04d", step)}}},
				}
				entries := make([]index.MessageEntry, 0, 2)
				for i, message := range additions {
					data, err := callrecord.Encode(message)
					require.NoError(t, err)
					hash, err := store.Store.Put(t.Context(), "root", data)
					require.NoError(t, err)
					entries = append(entries, index.MessageEntry{ID: index.MessageID(len(view) + i + 1), Kind: index.Message, Hash: hash, Role: message.Role, Bytes: len(message.Parts[0].(model.TextPart).Text)})
				}
				segment, err := index.NewSegment(head, chain, entries)
				require.NoError(t, err)
				data, _, err := index.EncodeSegment(segment)
				require.NoError(t, err)
				head, err = store.Store.Put(t.Context(), "root", data)
				require.NoError(t, err)
				chain = segment.Entries[len(segment.Entries)-1].Chain
				allEntries = append(allEntries, segment.Entries...)
				view = append(view, additions...)
				ref.IndexHead = head
				loadedMessages := view
				if mode == "real_compaction" {
					d := compaction.Decide(state, allEntries, ref, compaction.Settings{})
					state, ref = d.State, d.View
					if d.Tier != compaction.None {
						compacted++
					}
					loaded, err := rt.loadTestView(t.Context(), "root", "run", ref)
					require.NoError(t, err)
					loadedMessages = loaded.Messages
					if loaded.View != nil {
						ref.Checkpoint, ref.Pinned = &loaded.View.Checkpoint, loaded.View.Pinned
						state.Rendered = compaction.RenderedCheckpoint{Hash: string(*ref.Checkpoint), TailFrom: ref.TailFrom, Budget: ref.Budget, Bytes: loaded.View.CheckpointBytes}
					}
				}
				recorder := NewCallRecording(store, "root", ref, loadedMessages, step)
				seq, err := store.NextSeq(t.Context(), "root", "run")
				require.NoError(t, err)
				ctx := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: seq, Attempt: 1, ModelID: "fake"})
				var provider model.Client = &historyCountingClient{completeText: "response"}
				if mode == "real_compaction" {
					provider = &growthCompactionClient{historyCountingClient: &historyCountingClient{completeText: "response"}}
				}
				client := &callRecordingClient{inner: provider, recording: recorder}
				_, err = client.Complete(ctx, &model.Request{Model: "fake", Messages: loadedMessages})
				require.NoError(t, err)
				if mode == "real_compaction" {
					state = compaction.Observe(state, recorder.Facts())
				}
				points[step-1] = float64(store.total)
			}
			slope, residual := fitCallBytes(points)
			t.Logf("1000 steps mode=%s slope=%.3f bytes/step max_residual=%.3f bytes total=%d", mode, slope, residual, store.total)
			if mode == "real_compaction" {
				assert.Positive(t, compacted)
				t.Logf("compactions=%d", compacted)
			}
			assert.Greater(t, slope, 500.0)
			assert.Less(t, slope, 4000.0)
			assert.Less(t, residual, 16.0*1024)
		})
	}
}

func (s *callByteStore) PutCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	if err := s.Store.PutCall(ctx, scope, record); err != nil {
		return err
	}
	s.total += len(record.Started) + len(record.Completion)
	return nil
}

// Put counts every successful content write made by the recorder, even when
// an address already exists. View setup writes bypass this wrapper.
func (s *callByteStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	hash, err := s.Store.Put(ctx, scope, data)
	if err != nil {
		return "", err
	}
	s.total += len(data)
	return hash, nil
}

// fitCallBytes fits all cumulative recorder writes (content and call bodies) against step count and bounds
// the largest absolute residual, so a quadratic curve cannot pass as linear.
func fitCallBytes(points []float64) (float64, float64) {
	n := float64(len(points))
	meanX := (n + 1) / 2
	var meanY float64
	for _, y := range points {
		meanY += y / n
	}
	var numerator, denominator float64
	for i, y := range points {
		x := float64(i+1) - meanX
		numerator += x * (y - meanY)
		denominator += x * x
	}
	slope := numerator / denominator
	var residual float64
	for i, y := range points {
		predicted := meanY + slope*(float64(i+1)-meanX)
		residual = math.Max(residual, math.Abs(y-predicted))
	}
	return slope, residual
}

// growthCompactionClient gives the growth recorder real, measured model limits.
type growthCompactionClient struct{ *historyCountingClient }

func (c *growthCompactionClient) ContextLimits(context.Context, *model.Request) (model.ContextLimits, string, bool) {
	return model.ContextLimits{ContextWindow: 6000}, "fake", true
}

// seedGrowthView starts above the trigger, so the 1000-step fit measures steady
// compaction-on recorder growth rather than mixing two different per-call costs.
// The first request is still full; only its adopted facts enable the second cut.
func seedGrowthView(t *testing.T, store runstore.Store) ([]*model.Message, []index.MessageEntry, runstore.Hash, runstore.Hash) {
	t.Helper()
	messages := make([]*model.Message, 0, 2000)
	entries := make([]index.MessageEntry, 0, 2000)
	for i := 0; i < 1000; i++ {
		for _, role := range []model.ConversationRole{model.ConversationRoleUser, model.ConversationRoleAssistant} {
			text := fmt.Sprintf("seed turn %04d", i)
			message := &model.Message{Role: role, Parts: []model.Part{model.TextPart{Text: text}}}
			data, err := callrecord.Encode(message)
			require.NoError(t, err)
			hash, err := store.Put(t.Context(), "root", data)
			require.NoError(t, err)
			messages = append(messages, message)
			entries = append(entries, index.MessageEntry{ID: index.MessageID(len(entries) + 1), Kind: index.Message, Hash: hash, Role: role, Bytes: len(text)})
		}
	}
	segment, err := index.NewSegment("", "", entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(segment)
	require.NoError(t, err)
	head, err := store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	return messages, segment.Entries, head, segment.Entries[len(entries)-1].Chain
}
