package runrecord_test

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnadoptedCallContextsDoNotAdoptRunSettings(t *testing.T) {
	f := newRecordFixture()
	for i, disabled := range []bool{false, true} {
		settings := compaction.Merge(compaction.Settings{DisableRecall: disabled}, nil)
		fingerprint, err := f.store.Put(t.Context(), "root", compaction.Canonical(settings))
		require.NoError(t, err)
		checkpointHash := f.put(t, index.CheckpointRecord{Fingerprint: index.Fingerprint(fingerprint)})
		view := index.ViewRef{Checkpoint: &checkpointHash, Budget: index.RenderBudget{FramingTokens: 200, KeepUserTokens: 100, MaxSummaryTokens: 100, ReceiptTokens: 100}}
		messages, err := checkpoint.Project(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
			return f.store.Get(ctx, "root", hash)
		}, nil, view)
		require.NoError(t, err)
		request := &model.Request{Model: "fake", Messages: messages}
		parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", request, view, messages)
		require.NoError(t, err)
		key := runstore.CallKey{RunID: "run", Seq: i + 1, Attempt: 1}
		started, err := callrecord.Encode(callrecord.Started{Key: key, Context: string(fingerprint), Step: 1, Activity: callrecord.ActivityPlanner, Model: "fake", Request: parts})
		require.NoError(t, err)
		record := runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}
		require.NoError(t, f.store.PutCall(t.Context(), "root", record))
		if disabled {
			record.State = runstore.CallFailed
			record.Completion, err = callrecord.Encode(callrecord.Completion{Error: &callrecord.CallError{Kind: "canceled"}})
			require.NoError(t, err)
			require.NoError(t, f.store.PutCall(t.Context(), "root", record))
		}
	}
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	run := record.Runs[0]
	assert.Empty(t, run.ContextFingerprint)
	assert.Nil(t, run.ContextSettings)
	assert.False(t, run.Sealed)
	require.Len(t, run.Calls, 2)
	assert.Equal(t, runrecord.Incomplete, run.Calls[0].Class)
	assert.Equal(t, runrecord.Abandoned, run.Calls[1].Class)
	assert.NotEqual(t, run.Calls[0].Started.Context, run.Calls[1].Started.Context)
	assert.Len(t, run.Checkpoints, 2)
	for _, call := range run.Calls {
		assert.Contains(t, record.Content, runstore.Hash(call.Started.Context))
	}
	// A captured checkpoint listing must not bypass a later call's validation.
	started := run.Calls[0].Started
	started.Context = ""
	started.Key.Seq = 3
	body, err := callrecord.Encode(started)
	require.NoError(t, err)
	require.NoError(t, f.store.PutCall(t.Context(), "root", runstore.CallRecord{Key: started.Key, State: runstore.CallStarted, Started: body}))
	_, err = runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.ErrorContains(t, err, "fingerprint")
}

func TestContextFingerprintExportAndMismatch(t *testing.T) {
	for _, kind := range []string{"export", "call", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			f := newRecordFixture()
			settings := compaction.Merge(compaction.Settings{}, nil)
			bytes := compaction.Canonical(settings)
			hash, err := f.store.Put(t.Context(), "root", bytes)
			require.NoError(t, err)
			step := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, nil, recordedProtocolVersion, time.Unix(1, 0))
			step.ContextSettings = &settings
			step.ContextFingerprint = string(hash)
			if kind == "call" {
				step.CallKeys = []runstore.CallKey{f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")}
			}
			if kind == "checkpoint" {
				data, err := json.Marshal(index.CheckpointRecord{Fingerprint: "planted"})
				require.NoError(t, err)
				cp, err := f.store.Put(t.Context(), "root", data)
				require.NoError(t, err)
				step.ViewRef.Checkpoint = &cp
			}
			f.event(t, step)
			record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			if kind != "export" {
				require.ErrorContains(t, err, "fingerprint")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, string(hash), record.Runs[0].ContextFingerprint)
			assert.Equal(t, settings, *record.Runs[0].ContextSettings)
			assert.Equal(t, bytes, record.Content[hash])
		})
	}
}
