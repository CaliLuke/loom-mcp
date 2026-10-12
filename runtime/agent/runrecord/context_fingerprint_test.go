package runrecord_test

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
