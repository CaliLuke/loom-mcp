package fake_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamProbe struct {
	model.Client
	calls int
}

func (p *streamProbe) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	p.calls++
	return nil, errors.New("delegated stream")
}
func TestRejectStreamTrigger(t *testing.T) {
	for _, trigger := range []bool{false, true} {
		p := &streamProbe{}
		client := fake.Reject(p)
		text := "summarise normally"
		if trigger {
			text = "[[force-summary-rejection]]"
		}
		req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}}}
		stream, err := client.Stream(t.Context(), req)
		if !trigger {
			require.ErrorContains(t, err, "delegated stream")
			assert.Equal(t, 1, p.calls)
			continue
		}
		require.NoError(t, err)
		assert.Zero(t, p.calls)
		for {
			_, err = stream.Recv()
			if err != nil {
				break
			}
		}
		require.ErrorIs(t, err, io.EOF)
		require.NoError(t, stream.Finalize(nil))
		got := stream.Response()
		require.NotNil(t, got)
		text, err = fake.RequestText(&model.Request{Messages: []*model.Message{&got.Content[0]}})
		require.NoError(t, err)
		assert.Contains(t, text, "recall_context")
	}
}
