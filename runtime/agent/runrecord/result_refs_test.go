package runrecord_test

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadIgnoresToolResultEventRefs(t *testing.T) {
	for _, refs := range []tools.ResultRefs{{}, {Result: "missing"}, {ServerData: "missing"}, {Payload: "missing"}} {
		f := newRecordFixture()
		result := f.put(t, map[string]any{"full": "result"})
		server := f.put(t, map[string]any{"private": true})
		head := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.ToolResult, Hash: result, Record: []runstore.Hash{server}, Batch: 1, ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "preview"}}}})
		f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, nil, recordedProtocolVersion, time.Unix(1, 0)))
		before, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
		require.NoError(t, err)
		event := hooks.NewToolResultReceivedEvent("run", "agent", "", "tool", "call", "", nil, nil, nil, "preview", nil, 0, nil, nil, nil)
		event.Refs = refs
		f.event(t, event)
		after, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
		require.NoError(t, err, "observer refs never participate in reconstruction")
		assert.Equal(t, before.Runs[0].Results, after.Runs[0].Results)
		assert.Equal(t, before.Runs[0].ServerOnly, after.Runs[0].ServerOnly)
		assert.Equal(t, before.Runs[0].Transcript, after.Runs[0].Transcript)
		assert.Equal(t, before.Content, after.Content)
	}
}
