package runrecord_test

import (
	"encoding/json/v2"
	"maps"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonTextCompatibilityCanonicalExport(t *testing.T) {
	for _, fault := range []string{"none", "wrong scope", "wrong coverage", "wrong support", "missing fact", "before call adoption", "later replacement call", "missing decision evidence"} {
		t.Run(fault, func(t *testing.T) {
			f := newRecordFixture()
			entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: f.put(t, &model.Message{Role: "user", Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}}}), NonText: 1500}}
			head := f.segment(t, "", entries)
			key := f.call(t, "run", 1, 1, runstore.CallStarted, callrecord.ActivityPlanner, head)
			calls, err := f.store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			facts := contextmeasure.RequestFacts{Call: key, Model: "fake", Qualified: compaction.Evict, ViewBearing: true, ContextWindow: 200000, Outcome: contextmeasure.OutcomeOverflow, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 2}}
			terminal := calls[0]
			terminal.State = runstore.CallFailed
			terminal.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &facts, Error: &callrecord.CallError{Kind: "context_overflow", Message: "overflow"}})
			require.NoError(t, err)
			require.NoError(t, f.store.PutCall(t.Context(), "root", terminal))
			state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{facts})
			request := contextcompat.BuildRequest("root", "run", head, nil, entries, state)
			evidence := compaction.Compatibility{Coverage: request.Coverage, Support: request.Support, Checked: true, Compatible: true}
			fact := contextcompat.Fact{Request: request, Evidence: evidence}
			switch fault {
			case "wrong scope":
				fact.Request.Scope = "other"
			case "wrong coverage":
				fact.Request.Coverage = "wrong"
			case "wrong support":
				fact.Request.Support = "wrong"
			}
			result := contextcompat.Result{Ref: f.put(t, fact), Evidence: evidence}
			if fault == "missing fact" {
				result.Ref = "sha256:missing"
			}
			adoption := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, []runstore.CallKey{key}, 19, time.Unix(1, 0))
			settings := compaction.Settings{Default: true, KeepNonTextResident: true}
			adoption.ContextSettings = &settings
			if fault != "before call adoption" {
				decoded, err := callrecord.DecodeCompletion(terminal.Completion)
				require.NoError(t, err)
				failure := contextmeasure.ContextOverflow{Source: "provider", Failed: *decoded.Facts, Ordinal: 1}
				f.event(t, hooks.NewContextOverflowEvent(adoption, failure, "recovered", nil))
				later := f.call(t, "run", 2, 1, runstore.CallStarted, callrecord.ActivityPlanner, head)
				all, err := f.store.ListCalls(t.Context(), "root", "run")
				require.NoError(t, err)
				narrow := facts
				narrow.Call, narrow.Qualified, narrow.ToolResultParts, narrow.Outcome = later, "", model.ToolResultPartSupport{}, contextmeasure.OutcomeOK
				completed := all[1]
				completed.State = runstore.CallCompleted
				completed.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &narrow})
				require.NoError(t, err)
				require.NoError(t, f.store.PutCall(t.Context(), "root", completed))
				retry := hooks.NewPlannerStepEvent("run", "agent", "", 2, index.ViewRef{IndexHead: head}, []runstore.CallKey{later}, 19, time.Unix(2, 0))
				retry.ContextSettings = &settings
				f.event(t, retry)
			}
			if fault == "later replacement call" {
				later := f.call(t, "run", 3, 1, runstore.CallStarted, callrecord.ActivityPlanner, head)
				fact.Request.Calls = []runstore.CallKey{later}
				result.Ref = f.put(t, fact)
			}
			event := hooks.NewContextCompactionEvent("run", "agent", "", 3, hooks.ContextCompatibility, "", "", 0, 0, 0, 0, nil, time.Unix(2, 0))
			event.Compatibility = &result
			if fault != "missing decision evidence" {
				f.event(t, event)
			}
			if fault != "before call adoption" {
				next := hooks.NewPlannerStepEvent("run", "agent", "", 3, index.ViewRef{IndexHead: head}, nil, 19, time.Unix(3, 0))
				next.ContextSettings = &settings
				f.event(t, next)
			}
			if fault == "before call adoption" {
				f.event(t, adoption)
			}
			f.event(t, hooks.NewRunSealedEvent("run", head, 19, time.Unix(3, 0)))
			record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			if fault != "none" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, record.Content, result.Ref, "addressed compatibility evidence must be exported")
			data, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(data)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
			tampered := record.Export()
			tampered.Content = maps.Clone(tampered.Content)
			tampered.Content[result.Ref] = []byte(`{"altered":true}`)
			altered, err := tampered.Marshal()
			require.NoError(t, err)
			_, err = runrecord.FromExport(altered)
			require.Error(t, err, "standalone export must validate addressed compatibility evidence")
			var wire map[string]any
			require.NoError(t, json.Unmarshal(data, &wire))
			assert.NotEmpty(t, wire)
		})
	}
}
