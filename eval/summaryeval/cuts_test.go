package summaryeval

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutsOrigins(t *testing.T) {
	for _, tt := range []struct {
		name, reason, tier, skip string
		want                     string
	}{
		{"summary", "threshold", "summary", "", "summary"},
		{"rejected", "threshold", "evict", "rejected", "rejected"},
		{"receipt", "threshold", "evict", "", "receipt_only"},
		{"overflow", "overflow", "evict", "", ""},
		{"clear", "threshold", "clear", "", ""},
		{"stale", "threshold", "evict", "stale", ""},
		{"unknown", "threshold", "evict", "summariser_limits_unknown", ""},
		{"seed", "", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := runstore.Hash("checkpoint")
			previous := runstore.Hash("previous")
			r := runrecord.Run{RunID: "run", Checkpoints: []runrecord.Checkpoint{{Hash: h, Record: index.CheckpointRecord{Through: 8, Prev: &previous}}, {Hash: previous, Record: index.CheckpointRecord{Through: 3}}}}
			if tt.skip != "" {
				r.Events = append(r.Events, testEvent(t, hooks.ContextCompactionSkipped, hooks.ContextCompactionEvent{Step: 4, Reason: tt.skip, Kind: "missing_headings"}))
			}
			if tt.name != "seed" {
				r.Events = append(r.Events, testEvent(t, hooks.ContextCompacted, hooks.ContextCompactionEvent{Step: 4, Reason: tt.reason, Tier: tt.tier, Checkpoint: &h}))
			}
			cuts := Cuts(&runrecord.RunRecord{RootRunID: "run", Runs: []runrecord.Run{r}})
			if tt.want == "" {
				assert.Empty(t, cuts)
				return
			}
			require.Len(t, cuts, 1)
			assert.Equal(t, tt.want, cuts[0].Origin)
			assert.Equal(t, index.MessageID(4), cuts[0].From)
			assert.Equal(t, index.MessageID(9), cuts[0].To)
		})
	}
}

func testEvent(t *testing.T, kind hooks.EventType, value any) *runlog.Event {
	t.Helper()
	data, err := encode(value)
	require.NoError(t, err)
	return &runlog.Event{RunID: "run", AgentID: "agent", SessionID: "session", TurnID: "turn", Type: kind, Payload: data}
}

func TestCutsRejectedBeforeDisableNotice(t *testing.T) {
	rec := recordedFixture(t)
	r := &rec.Runs[0]
	h := r.Checkpoints[0].Hash
	r.Events = append(r.Events[:1], testEvent(t, hooks.ContextCompactionSkipped, hooks.ContextCompactionEvent{Step: 9, Reason: "rejected", Kind: "missing_headings"}), testEvent(t, hooks.ContextCompactionSkipped, hooks.ContextCompactionEvent{Step: 9, Reason: "summaries_disabled"}), testEvent(t, hooks.ContextCompacted, hooks.ContextCompactionEvent{Step: 9, Reason: "threshold", Tier: "evict", Checkpoint: &h}))
	cuts := Cuts(rec)
	require.Len(t, cuts, 1)
	assert.Equal(t, "rejected", cuts[0].Origin)
	assert.Equal(t, "missing_headings", cuts[0].Old.Kind)
}
