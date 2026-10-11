package runtime

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
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
	for _, mode := range []string{"off", "keep_recent", "compress"} {
		t.Run(mode, func(t *testing.T) {
			store := &callByteStore{Store: inmem.New()}
			policy := growthHistoryPolicy(mode)
			var view []*model.Message
			var head, chain runstore.Hash
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
					entries = append(entries, index.MessageEntry{ID: index.MessageID(len(view) + i + 1), Kind: index.Message, Hash: hash})
				}
				segment, err := index.NewSegment(head, chain, entries)
				require.NoError(t, err)
				data, _, err := index.EncodeSegment(segment)
				require.NoError(t, err)
				head, err = store.Store.Put(t.Context(), "root", data)
				require.NoError(t, err)
				chain = segment.Entries[len(segment.Entries)-1].Chain
				view = append(view, additions...)
				recorder := NewCallRecording(store, "root", index.ViewRef{IndexHead: head}, view, step)
				filtered, err := policy(t.Context(), view, nil)
				require.NoError(t, err)
				seq, err := store.NextSeq(t.Context(), "root", "run")
				require.NoError(t, err)
				ctx := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: seq, Attempt: 1, ModelID: "fake"})
				client := &callRecordingClient{inner: &historyCountingClient{completeText: "response"}, recording: recorder}
				_, err = client.Complete(ctx, &model.Request{Model: "fake", Messages: filtered})
				require.NoError(t, err)
				points[step-1] = float64(store.total)
			}
			slope, residual := fitCallBytes(points)
			t.Logf("1000 steps mode=%s slope=%.3f bytes/step max_residual=%.3f bytes total=%d", mode, slope, residual, store.total)
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

func growthHistoryPolicy(mode string) HistoryPolicy {
	switch mode {
	case "keep_recent":
		return KeepRecentTurns(2)
	case "compress":
		return Compress(&historyCountingClient{completeText: "bounded summary"}, HistoryCompressionConfig{CompressAtTurns: 8, KeepMaxTurns: 2})
	default:
		return func(_ context.Context, messages []*model.Message, _ []*model.ToolDefinition) ([]*model.Message, error) {
			return messages, nil
		}
	}
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
