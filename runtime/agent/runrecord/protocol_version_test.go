package runrecord_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadRecordedProtocolVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kinds   []hooks.EventType
		version int
	}{
		{"step only", []hooks.EventType{hooks.PlannerStep}, 17},
		{"seal only", []hooks.EventType{hooks.RunSealed}, 23},
		{"step and seal", []hooks.EventType{hooks.PlannerStep, hooks.PlannerStep, hooks.RunSealed}, 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecordFixture()
			for i, kind := range tc.kinds {
				appendProtocolEvent(t, f, kind, tc.version, i+1)
			}
			record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			require.NoError(t, err)
			assert.Equal(t, tc.version, record.Runs[0].WorkflowProtocolVersion)
			data, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(data)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

func TestReadRejectsConflictingProtocolVersions(t *testing.T) {
	for _, first := range []hooks.EventType{hooks.PlannerStep, hooks.RunSealed} {
		for _, second := range []hooks.EventType{hooks.PlannerStep, hooks.RunSealed} {
			t.Run(string(first)+"/"+string(second), func(t *testing.T) {
				f := newRecordFixture()
				appendProtocolEvent(t, f, first, 17, 1)
				appendProtocolEvent(t, f, second, 23, 2)
				record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.ErrorContains(t, err, "conflicting workflow protocol versions")
				assert.Nil(t, record)
			})
		}
	}
}

func TestReadProtocolUnknownWithoutRecordEvent(t *testing.T) {
	for _, state := range []runstore.CallState{"", runstore.CallStarted, runstore.CallCompleted} {
		t.Run(string(state), func(t *testing.T) {
			f := newRecordFixture()
			if state != "" {
				f.call(t, "run", 9, 1, state, callrecord.ActivityPlanner, "")
				f.event(t, hooks.NewPlannerNoteEvent("run", "agent", "", "ordinary event", nil))
			}
			record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			require.NoError(t, err)
			assert.Zero(t, record.Runs[0].WorkflowProtocolVersion)
			data, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(data)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

// appendProtocolEvent supplies explicit durable stamps independent of the
// current workflow constant, so this reader proof survives future bumps.
func appendProtocolEvent(t *testing.T, f recordFixture, kind hooks.EventType, version, step int) {
	t.Helper()
	payload := map[string]any{"ProtocolVersion": version, "IndexHead": ""}
	if kind == hooks.PlannerStep {
		payload["Step"] = step
		payload["ViewRef"] = index.ViewRef{}
	}
	data, err := callrecord.Encode(payload)
	require.NoError(t, err)
	_, err = f.log.Append(t.Context(), &runlog.Event{RunID: "run", EventKey: fmt.Sprintf("record:%d", step), Type: kind, Payload: data, Timestamp: time.Unix(int64(step), 0)})
	require.NoError(t, err)
}
