package runrecord_test

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverflowRecordRejectsUnsafePresentationAndRecovery(t *testing.T) {
	for _, fault := range []string{"none", "unsafe presentation", "adapter target", "unchanged view", "budget retained"} {
		t.Run(fault, func(t *testing.T) {
			f := newRecordFixture()
			entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 60000, Hash: f.put(t, textMessage("large history"))}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 1, Hash: f.put(t, []*model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "answer"}}}})}}
			head := f.segment(t, "", entries)
			key := f.call(t, "run", 1, 1, runstore.CallStarted, callrecord.ActivityPlanner, head)
			calls, err := f.store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			facts := contextmeasure.RequestFacts{Call: key, Model: "small", ViewBearing: true, ContextWindow: 8000, TextBytes: 60001, ViewTextBytes: 60001, Outcome: contextmeasure.OutcomeOverflow}
			record := calls[0]
			record.State = runstore.CallFailed
			record.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &facts, Error: &callrecord.CallError{Kind: "context_overflow", Message: "too large"}})
			require.NoError(t, err)
			require.NoError(t, f.store.PutCall(t.Context(), "root", record))
			settings := compaction.ContextSettings{GuardMargin: contextmeasure.Rate{Tokens: 1, Bytes: 10}}
			adoption := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{IndexHead: head}, []runstore.CallKey{key}, 9, time.Unix(1, 0))
			adoption.ContextSettings = &settings
			failure := contextmeasure.ContextOverflow{Source: "provider", Failed: facts, Ordinal: 1, ViewTextBytes: facts.ViewTextBytes}
			recovery := compaction.Recover(compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{facts}), entries, adoption.ViewRef, failure, settings.Settings)
			switch fault {
			case "adapter target":
				recovery.After++
			case "unchanged view":
				recovery.View = adoption.ViewRef
			case "budget retained":
				recovery.View.Budget.MaxSummaryTokens = 16000
			}
			f.event(t, hooks.NewContextOverflowEvent(adoption, failure, "recovered", &recovery))
			if fault == "unsafe presentation" {
				presentation := hooks.NewAssistantPresentationCommittedEvent("run", "agent", "", []string{"overflow attempt"}, []*model.Message{textMessage("unsafe")})
				presentation.CallKeys = []*runstore.CallKey{&key}
				f.event(t, presentation)
			}
			recordOutput, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			switch fault {
			case "none":
				require.NoError(t, err)
				assert.Empty(t, recordOutput.Runs[0].Presentations)
			case "unsafe presentation":
				require.ErrorContains(t, err, "presentation committed from overflow attempt")
			default:
				require.ErrorContains(t, err, "recovery differs from recorded facts")
			}
		})
	}
}
