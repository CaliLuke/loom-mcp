package trajectory_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPrefersRecordIdentity(t *testing.T) {
	observer := &runlog.Event{Type: hooks.PlannerNote, AgentID: "observer-agent", SessionID: "observer-session", Payload: []byte(`{}`)}
	record := &runlog.Event{Type: hooks.RunSealed, AgentID: "record-agent", SessionID: "record-session"}
	anonymous := &runlog.Event{Type: hooks.RunSealed, AgentID: "record-agent"}
	for _, tc := range []struct {
		name           string
		events         []*runlog.Event
		agent, session string
	}{
		{"observer first", []*runlog.Event{observer, record}, "record-agent", "record-session"},
		{"record first", []*runlog.Event{record, observer}, "record-agent", "record-session"},
		{"observer only", []*runlog.Event{observer}, "observer-agent", "observer-session"},
		{"record without session", []*runlog.Event{observer, anonymous}, "record-agent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &runrecord.RunRecord{Runs: []runrecord.Run{{RunID: "run", Sealed: true, Events: tc.events}}}
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			assert.Equal(t, tc.agent, string(got.AgentID))
			assert.Equal(t, tc.session, got.SessionID)
		})
	}
}
