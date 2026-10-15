package runrecord_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordReadFailure struct {
	runstore.Store
	reads  int
	failAt int
}

func TestRunRecordReadFailsAtomicallyAtEveryContentRead(t *testing.T) {
	f := newRecordFixture()
	result := f.put(t, map[string]any{"value": "result"})
	server := f.put(t, map[string]any{"secret": "private"})
	payload := f.put(t, map[string]any{"query": "query"})
	call := f.put(t, map[string]any{"call": "metadata"})
	output := f.put(t, planner.ToolOutput{Refs: tools.ResultRefs{Result: result, ServerData: server, Payload: payload}, Call: call})
	answer, err := f.store.Put(t.Context(), "root", []byte("clarified"))
	require.NoError(t, err)
	head := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.Message, Hash: f.put(t, textMessage("user"))},
		{ID: 2, Kind: index.TypedInput, Hash: f.put(t, map[string]any{"answer": true}), InputID: "typed"},
		{ID: 3, Kind: index.Clarification, Hash: answer},
		{ID: 4, Kind: index.ToolResult, Hash: result, Record: []runstore.Hash{server}, Output: output, ToolUseIDs: []string{"tool"}, Batch: 4, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "preview"}}},
	})
	key := f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, head)
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, []runstore.CallKey{key}, recordedProtocolVersion, time.Unix(1, 0)))
	baseline := &recordReadFailure{Store: f.store}
	record, err := runrecord.Read(t.Context(), baseline, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	require.NotEmpty(t, record.Runs[0].Results)
	require.Positive(t, baseline.reads)
	for failAt := 1; failAt <= baseline.reads; failAt++ {
		t.Run(fmt.Sprintf("read %d", failAt), func(t *testing.T) {
			store := &recordReadFailure{Store: f.store, failAt: failAt}
			record, err := runrecord.Read(t.Context(), store, f.log, "root", "run")
			require.ErrorIs(t, err, assert.AnError)
			assert.Nil(t, record)
		})
	}
}

func (s *recordReadFailure) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.reads++
	if s.reads == s.failAt {
		return nil, assert.AnError
	}
	return s.Store.Get(ctx, scope, hash)
}
