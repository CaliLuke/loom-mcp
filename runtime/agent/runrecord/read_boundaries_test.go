package runrecord_test

import (
	"context"
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

type boundaryRecordLog struct {
	runlog.Store
	page runlog.Page
	err  error
}

type boundaryCallStore struct {
	runstore.Store
	calls []runstore.CallRecord
	err   error
}

func TestRunRecordRejectsCorruptEventStorage(t *testing.T) {
	for _, tt := range []struct {
		name string
		page runlog.Page
		err  error
		want string
	}{
		{"nil event", runlog.Page{Events: []*runlog.Event{nil}}, nil, "invalid event run identity"},
		{"foreign run", runlog.Page{Events: []*runlog.Event{{RunID: "other"}}}, nil, "invalid event run identity"},
		{"stuck cursor", runlog.Page{NextCursor: "same"}, nil, "pagination did not advance"},
		{"backend failure", runlog.Page{}, assert.AnError, "list events"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRecordFixture()
			record, err := runrecord.Read(t.Context(), fixture.store, boundaryRecordLog{Store: fixture.log, page: tt.page, err: tt.err}, "root", "run")
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, record)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
			}
		})
	}
}

func TestRunRecordRejectsMalformedCanonicalEvents(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.ContinuationStarted, hooks.ChildRunLinked, hooks.ContextOverflow, hooks.PlannerStep, hooks.ContextRecalled, hooks.RunSealed, hooks.ContextCompacted, hooks.ContextCompactionSkipped, hooks.AssistantTurnCommitted, hooks.ContextResidencyAdopted, hooks.ContextCompatibility} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newRecordFixture()
			log := boundaryRecordLog{Store: fixture.log, page: runlog.Page{Events: []*runlog.Event{{RunID: "run", EventKey: "bad", Type: kind, Payload: []byte("{")}}}}
			record, err := runrecord.Read(t.Context(), fixture.store, log, "root", "run")
			require.Error(t, err)
			assert.Nil(t, record)
		})
	}
}

func TestRunRecordRejectsInvalidAdoptedKeysAndCheckpoints(t *testing.T) {
	for _, key := range []runstore.CallKey{{RunID: "other", Seq: 1, Attempt: 1}, {RunID: "run", Seq: 0, Attempt: 1}, {RunID: "run", Seq: 1, Attempt: 0}} {
		fixture := newRecordFixture()
		fixture.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{key}, recordedProtocolVersion, time.Unix(1, 0)))
		record, err := runrecord.Read(t.Context(), fixture.store, fixture.log, "root", "run")
		require.ErrorContains(t, err, "invalid adopted key")
		assert.Nil(t, record)
	}
	for _, tt := range []struct {
		name       string
		checkpoint any
		want       string
	}{
		{"malformed", map[string]any{"future": true}, "decode checkpoint"},
		{"missing predecessor", index.CheckpointRecord{Prev: new(runstore.Hash("absent"))}, "not found"},
		{"missing origin scope", index.CheckpointRecord{From: &index.RunCheckpoint{Run: "prior", Hash: "hash"}}, "requires scope and run"},
		{"missing origin run", index.CheckpointRecord{From: &index.RunCheckpoint{Scope: "root", Hash: "hash"}}, "requires scope and run"},
		{"missing summary", index.CheckpointRecord{Summary: new(runstore.Hash("absent"))}, "not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRecordFixture()
			hash := fixture.put(t, tt.checkpoint)
			fixture.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{Checkpoint: &hash}, nil, recordedProtocolVersion, time.Unix(1, 0)))
			record, err := runrecord.Read(t.Context(), fixture.store, fixture.log, "root", "run")
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, record)
		})
	}
}

func TestRunRecordRejectsCorruptCallStorage(t *testing.T) {
	fixture := newRecordFixture()
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	wrong, err := callrecord.Encode(callrecord.Started{Key: runstore.CallKey{RunID: "other", Seq: 1, Attempt: 1}})
	require.NoError(t, err)
	started, err := callrecord.Encode(callrecord.Started{Key: key})
	require.NoError(t, err)
	completed, err := callrecord.Encode(callrecord.Completion{Response: new(runstore.Hash("missing"))})
	require.NoError(t, err)
	for _, tt := range []struct {
		name   string
		record runstore.CallRecord
		want   string
	}{
		{"invalid state", runstore.CallRecord{Key: key, State: "future", Started: started}, "state"},
		{"wrong started key", runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: wrong}, "identity differs"},
		{"malformed completion", runstore.CallRecord{Key: key, State: runstore.CallCompleted, Started: started, Completion: []byte("{")}, "decode callrecord completion"},
		{"missing response", runstore.CallRecord{Key: key, State: runstore.CallCompleted, Started: started, Completion: completed}, "not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := boundaryCallStore{Store: fixture.store, calls: []runstore.CallRecord{tt.record}}
			record, err := runrecord.Read(t.Context(), store, fixture.log, "root", "run")
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, record)
		})
	}
	record, err := runrecord.Read(t.Context(), boundaryCallStore{Store: fixture.store, err: assert.AnError}, fixture.log, "root", "run")
	require.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, record)
}

func (s boundaryRecordLog) List(context.Context, string, string, int) (runlog.Page, error) {
	return s.page, s.err
}

func (s boundaryCallStore) ListCalls(context.Context, string, string) ([]runstore.CallRecord, error) {
	return s.calls, s.err
}
