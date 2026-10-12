package trajectory_test

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildObservedPayloadsAndVerification(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.ThinkingBlock, hooks.PlannerNote, hooks.RetryHintIssued, hooks.PolicyDecision, hooks.RunPhaseChanged, hooks.PromptRendered, hooks.ToolAuthorization} {
		t.Run(string(kind), func(t *testing.T) {
			for _, unverified := range []bool{false, true} {
				f := newFixture()
				payload := rawjson.Message(`{"large":9007199254740993,"detail":"retained"}`)
				_, err := f.log.Append(t.Context(), &runlog.Event{RunID: "run", EventKey: "observed", Type: kind, Payload: payload, Timestamp: time.Unix(1, 0)})
				require.NoError(t, err)
				f.event(t, hooks.NewRunSealedEvent("run", "", api.WorkflowProtocolVersion, time.Unix(2, 0)), 2)
				rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.NoError(t, err)
				got, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: unverified})
				require.NoError(t, err)
				require.Len(t, got.Steps, 1)
				assert.Equal(t, kind, got.Steps[0].Event.Type)
				assert.Equal(t, payload, got.Steps[0].Event.Payload)
				assert.Equal(t, !unverified, got.Steps[0].Verified)
				assert.Zero(t, got.Totals.WallTime)
			}
		})
	}
}

func TestBuildWallTimeUsesOnlyRecordEvents(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.PlannerStep, hooks.RunSealed, hooks.ChildRunLinked, hooks.ContextCompacted, hooks.ContextCompactionSkipped, hooks.ContextOverflow, hooks.ContextRecalled, hooks.ContinuationStarted, hooks.ContinuationPublished, hooks.ContinuationSuperseded} {
		t.Run(string(kind), func(t *testing.T) {
			rec := &runrecord.RunRecord{Runs: []runrecord.Run{{RunID: "run", Sealed: true, Events: []*runlog.Event{
				{Type: hooks.RunStarted, Timestamp: time.Unix(1, 0)},
				{Type: kind, Payload: rawjson.Message(`{"Step":1,"ToolCallID":"a","ChildRunID":"child"}`), Timestamp: time.Unix(2, 0)},
				{Type: hooks.RunSealed, Timestamp: time.Unix(5, 0)},
				{Type: hooks.RunCompleted, Payload: rawjson.Message(`{"status":"success"}`), Timestamp: time.Unix(9, 0)},
			}}, {RunID: "child", Sealed: true}}, Content: nil}
			if kind == hooks.ChildRunLinked {
				rec = treeRecord(t)
				rec.Runs[0].Events = []*runlog.Event{
					{Type: kind, Payload: rawjson.Message(`{"ToolCallID":"a","ChildRunID":"child-a"}`), Timestamp: time.Unix(2, 0)},
					{Type: hooks.RunSealed, Timestamp: time.Unix(5, 0)},
				}
			}
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			assert.Equal(t, 3*time.Second, got.Totals.WallTime)
		})
	}
}

func TestBuildObservedEventPublicationSteps(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.ThinkingBlock, hooks.PromptRendered, hooks.PlannerNote, hooks.AwaitClarification, hooks.AwaitQuestions, hooks.AwaitTypedInput, hooks.AwaitConfirmation, hooks.AwaitExternalTools, hooks.RunPhaseChanged, hooks.ToolAuthorization, hooks.PolicyDecision, hooks.RetryHintIssued} {
		for _, test := range []struct {
			name    string
			payload rawjson.Message
			step    int
		}{
			{"unstamped", rawjson.Message(`{"large":9007199254740993}`), 1},
			{"stamped", rawjson.Message(`{"planner_step":2,"large":9007199254740993}`), 2},
		} {
			t.Run(string(kind)+"/"+test.name, func(t *testing.T) {
				rec := &runrecord.RunRecord{Runs: []runrecord.Run{{RunID: "run", Sealed: true, Events: []*runlog.Event{
					{Type: hooks.PlannerStep, Payload: rawjson.Message(`{"Step":1}`)},
					{Type: kind, Payload: test.payload},
					{Type: hooks.PlannerStep, Payload: rawjson.Message(`{"Step":2}`)},
				}}}}
				got, err := trajectory.Build(rec, "run", trajectory.Options{})
				require.NoError(t, err)
				require.Len(t, got.Steps, 1)
				assert.Equal(t, kind, got.Steps[0].Event.Type)
				assert.Equal(t, test.step, got.Steps[0].Step)
			})
		}
	}
}
