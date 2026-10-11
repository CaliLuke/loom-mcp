package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentContextRequiresRecording(t *testing.T) {
	assert.PanicsWithValue(t, "newAgentContext: recording is required", func() {
		newAgentContext(agentContextOptions{runtime: New()})
	})
}

func TestCallRecordingCommittedViewSize(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume_%t", resume), func(t *testing.T) {
			sizes := make([]int, 0, 2)
			for _, count := range []int{1, 50} {
				var messages []*model.Message
				rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
					client, ok := agent.ModelClient("registered-model")
					require.True(t, ok)
					_, err := client.Complete(ctx, &model.Request{Messages: messages})
					return err
				})
				data := []byte(`{"result":"pending"}`)
				hash, err := rt.RunStore.Put(t.Context(), "root-run", data)
				require.NoError(t, err)
				entries := make([]index.MessageEntry, count)
				for i := range entries {
					entries[i] = index.MessageEntry{ID: index.MessageID(i + 1), Kind: index.ToolResult, Batch: 1, Hash: hash, Bytes: len(data), ToolUseIDs: []string{fmt.Sprintf("call-%d", i)}}
				}
				segment, err := index.NewSegment("", "", entries)
				require.NoError(t, err)
				input.View.Pending = segment.Entries
				loaded := testPlannerView(t, rt, input)
				messages = loaded.Messages
				ctx := engine.WithActivityAttempt(t.Context(), 1)
				var out *PlanActivityOutput
				if resume {
					out, err = rt.PlanResumeActivity(ctx, input)
				} else {
					out, err = rt.PlanStartActivity(ctx, input)
				}
				require.NoError(t, err)
				calls, err := rt.RunStore.ListCalls(ctx, "root-run", "child-run")
				require.NoError(t, err)
				require.Len(t, calls, 1)
				started, err := callrecord.DecodeStarted(calls[0].Started)
				require.NoError(t, err)
				require.NotNil(t, started.Request.View)
				assert.Nil(t, started.Request.View.Pending)
				assert.Equal(t, loaded.Head, started.Request.View.IndexHead)
				assert.Equal(t, loaded.Head, out.IndexHead)
				assert.NotEmpty(t, out.IndexHead)
				encoded, err := callrecord.Encode(started.Request.View)
				require.NoError(t, err)
				sizes = append(sizes, len(encoded))
				assert.Len(t, input.View.Pending, count, "caller input remains owned by caller")
			}
			assert.Equal(t, sizes[0], sizes[1])
		})
	}
}
