package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonTextResidencyAdoptionRejectsAlteredProvenance(t *testing.T) {
	record := runResidencyBoundary(t, residencyBoundaryCase{window: 100000, resident: true, reason: "provider", interception: "drop"})
	baseline, err := record.Export().Marshal()
	require.NoError(t, err)
	for _, fault := range []string{"missing", "duplicate", "late", "future step", "scope", "coverage", "support", "reason", "witness"} {
		t.Run(fault, func(t *testing.T) {
			altered, err := runrecord.FromExport(baseline)
			require.NoError(t, err)
			events := altered.Runs[0].Events
			position := slices.IndexFunc(events, func(e *runlog.Event) bool {
				return e.Type == hooks.ContextResidencyAdopted
			})
			require.NotEqual(t, -1, position, "actual decision needs protected adoption")
			event := events[position]
			var adoption hooks.ContextResidencyEvent
			require.NoError(t, json.Unmarshal(event.Payload, &adoption))
			switch fault {
			case "missing":
				altered.Runs[0].Events = slices.Delete(events, position, position+1)
			case "duplicate":
				altered.Runs[0].Events = slices.Insert(events, position+1, event)
			case "late":
				altered.Runs[0].Events = append(slices.Delete(events, position, position+1), event)
			case "future step":
				adoption.Step++
			case "scope":
				adoption.RootRunID = "foreign"
			case "coverage":
				adoption.Coverage = "wrong"
			case "support":
				adoption.Support = "wrong"
			case "reason":
				adoption.Reason = "too_large"
			case "witness":
				adoption.Model = "other"
			}
			event.Payload, err = json.Marshal(adoption)
			require.NoError(t, err)
			data, err := altered.Export().Marshal()
			require.NoError(t, err)
			_, err = runrecord.FromExport(data)
			require.Error(t, err)
		})
	}
}

func TestNonTextResidencyStateFollowsProtectedAppend(t *testing.T) {
	for _, rejectAppend := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAppend), func(t *testing.T) {
			failure := errors.New("residency publication failed")
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, in *BeforeEventInput) (*BeforeEventDecision, error) {
				if in.Event.Type() == hooks.ContextNonTextResident {
					return nil, failure
				}
				return nil, nil
			}}))
			if rejectAppend {
				rt.RunEventStore = residencyRejectedLog{Store: rt.RunEventStore, err: failure}
			}
			base := compactionFixture(t, rt.RunStore, 3)
			base.contextSettings = compaction.Settings{Default: true, KeepNonTextResident: true}
			fact := contextmeasure.RequestFacts{Model: "m", Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, ViewBearing: true, Qualified: compaction.Evict, ContextWindow: 100000}
			persistResidentCall(t, rt.RunStore, fact)
			base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{fact})
			require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "user", Hash: compactionPut(t, rt.RunStore, &model.Message{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}}), NonText: 1500}))
			wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt}
			input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
			require.ErrorIs(t, rt.preparePlanActivity(wf, base, engine.ActivityOptions{}, &input, time.Time{}), failure)
			assert.Equal(t, !rejectAppend, base.compactionState.Flags.NonTextResident)
			assert.Equal(t, !rejectAppend, base.compactionFlags.NonTextResident)
		})
	}
}

func TestNonTextActivityResidencyRequiresCompatibilityAdoption(t *testing.T) {
	record := runResidencyBoundary(t, residencyBoundaryCase{window: 100000, support: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 100}, resident: true, reason: "too_large", scans: 1, interception: "drop", failure: "before model"})
	events := record.Runs[0].Events
	position := slices.IndexFunc(events, func(e *runlog.Event) bool {
		return e.Type == hooks.ContextCompatibility
	})
	require.NotEqual(t, -1, position)
	record.Runs[0].Events = slices.Delete(events, position, position+1)
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	_, err = runrecord.FromExport(data)
	require.Error(t, err, "protected residency cannot replace exact compatibility provenance")
}
