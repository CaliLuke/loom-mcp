package trajectory_test

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildReadExportParityAndEncoding(t *testing.T) {
	rec := treeRecord(t)
	data, err := rec.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	first, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	second, err := trajectory.Build(restored, "run", trajectory.Options{})
	require.NoError(t, err)
	assert.Equal(t, first, second)
	encoded, err := first.Marshal()
	require.NoError(t, err)
	independent := treeRecord(t)
	// Independent stores and maps, with a different call enumeration order.
	slices.Reverse(independent.Runs[0].Calls)
	rebuilt, err := trajectory.Build(independent, "run", trajectory.Options{})
	require.NoError(t, err)
	again, err := rebuilt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, encoded, again)
	child, err := trajectory.Build(rec, "child-a", trajectory.Options{})
	require.NoError(t, err)
	session := trajectory.Session{child, first}
	sessionBytes, err := session.Marshal()
	require.NoError(t, err)
	var ordered []struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(sessionBytes, &ordered))
	assert.Equal(t, "child-a", ordered[0].RunID)
	assert.Equal(t, "run", ordered[1].RunID)
}

func TestBuildCausalOrderAndParallelChildren(t *testing.T) {
	rec := treeRecord(t)
	// Store enumeration and child completion order cannot change causal order.
	slices.Reverse(rec.Runs[0].Calls)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	steps := recordSteps(got)
	require.Len(t, steps, 6)
	require.Equal(t, []trajectory.StepKind{trajectory.StepModel, trajectory.StepModel, trajectory.StepModel, trajectory.StepTool, trajectory.StepTool, trajectory.StepModel}, stepKinds(steps))
	assert.Equal(t, []int{1, 2, 3, 4}, []int{steps[0].Model.Key.Seq, steps[1].Model.Key.Seq, steps[2].Model.Key.Seq, steps[5].Model.Key.Seq})
	assert.Equal(t, []int{1, 1, 1, 1, 1, 2}, []int{steps[0].Step, steps[1].Step, steps[2].Step, steps[3].Step, steps[4].Step, steps[5].Step})
	assert.Equal(t, runrecord.Abandoned, steps[0].Model.Class)
	assert.Equal(t, "provider_failure", steps[0].Model.Error.Kind)
	assert.Equal(t, runrecord.Incomplete, steps[2].Model.Class)
	assert.Nil(t, steps[2].Model.Error)
	assert.Equal(t, "a", steps[3].Tool.ToolCallID)
	assert.Equal(t, "b", steps[4].Tool.ToolCallID)
	assert.Equal(t, "parent", steps[3].Tool.ParentToolCallID)
	require.Len(t, steps[3].Tool.Children, 1)
	require.Len(t, steps[4].Tool.Children, 1)
	assert.Equal(t, "child-a", steps[3].Tool.Children[0].RunID)
	assert.Equal(t, "child-b", steps[4].Tool.Children[0].RunID)
	assert.Equal(t, 25*time.Millisecond, steps[3].Tool.Duration)
	assert.Equal(t, `{"a":1,"z":9007199254740993}`, string(steps[4].Tool.Arguments))
	assert.JSONEq(t, `{"answer":2}`, string(steps[4].Tool.Result))
	assert.NotNil(t, steps[4].Tool.Error)
	assert.Equal(t, steps[1].Model.Chosen[1].Payload, steps[4].Tool.Arguments)
}

func TestBuildTotalsAndFinal(t *testing.T) {
	got, err := trajectory.Build(treeRecord(t), "run", trajectory.Options{})
	require.NoError(t, err)
	assert.Equal(t, 4, got.Totals.ModelCalls)
	assert.Equal(t, 2, got.Totals.AdoptedModelCalls)
	assert.Equal(t, map[runrecord.CallClass]int{runrecord.Adopted: 2, runrecord.Abandoned: 1, runrecord.Incomplete: 1}, got.Totals.ModelCallsByClass)
	assert.Equal(t, model.TokenUsage{Model: "resolved", InputTokens: 6, OutputTokens: 9, CacheReadTokens: 12, CacheWriteTokens: 15, TotalTokens: 42}, got.Totals.TokensByModel["resolved"])
	assert.Equal(t, 2, got.Totals.ToolCalls)
	assert.Equal(t, 1, got.Totals.FailedToolCalls)
	assert.Equal(t, 1, got.Totals.RepeatedToolCalls)
	assert.Equal(t, 5*time.Second, got.Totals.WallTime)
	assert.Equal(t, "success", got.Status)
	assert.True(t, got.StatusVerified)
	assert.Equal(t, "session", got.SessionID)
	assert.Equal(t, "agent", string(got.AgentID))
	require.NotNil(t, got.Final)
	assert.Equal(t, model.TextPart{Text: "final"}, got.Final.Parts[0])
}

func TestBuildFinalOutputPrecedence(t *testing.T) {
	for _, test := range []struct {
		name  string
		final *model.Message
	}{
		{name: "provided", final: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "override"}}}},
		{name: "explicit nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := trajectory.Build(treeRecord(t), "run", trajectory.Options{Output: &api.RunOutput{Final: test.final}})
			require.NoError(t, err)
			assert.Equal(t, test.final, got.Final)
		})
	}
}

func TestBuildRejectsInvalidRecords(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*runrecord.RunRecord)
		message string
	}{
		{"unsealed", func(rec *runrecord.RunRecord) { rec.Runs[0].Sealed = false }, "terminal"},
		{"orphaned result", func(rec *runrecord.RunRecord) { rec.Runs[0].Entries[0].Calls = nil }, "no matching call"},
		{"duplicate result", func(rec *runrecord.RunRecord) {
			duplicate := rec.Runs[0].Entries[1]
			duplicate.ID = 5
			rec.Runs[0].Entries = append(rec.Runs[0].Entries, duplicate)
		}, "duplicate result"},
		{"mismatched name", func(rec *runrecord.RunRecord) {
			rec.Content[rec.Runs[0].Entries[1].Output] = []byte(`{"Name":"wrong.tool","ToolCallID":"b"}`)
		}, "name mismatch"},
		{"duplicate call", func(rec *runrecord.RunRecord) {
			rec.Runs[0].Entries[0].Calls = append(rec.Runs[0].Entries[0].Calls, rec.Runs[0].Entries[0].Calls[0])
		}, "called twice"},
		{"duplicate call across assistant entries", func(rec *runrecord.RunRecord) {
			duplicate := rec.Runs[0].Entries[0]
			duplicate.ID = 5
			rec.Runs[0].Entries = append(rec.Runs[0].Entries, duplicate)
		}, "called twice"},
		{"missing child", func(rec *runrecord.RunRecord) { rec.Runs = rec.Runs[:1] }, "missing run"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := treeRecord(t)
			test.change(rec)
			_, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.ErrorContains(t, err, test.message)
			if test.name == "unsealed" {
				assert.ErrorIs(t, err, trajectory.ErrNotTerminal)
			}
		})
	}
}

func TestBuildObserverLossPreservesRecordSteps(t *testing.T) {
	rec := treeRecord(t)
	full, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: true})
	require.NoError(t, err)
	// This is the exact post-BeforeEvent evidence boundary: only record events survive.
	rec.Runs[0].Events = slices.DeleteFunc(rec.Runs[0].Events, func(event *runlog.Event) bool { return !hooks.IsRecordEvent(event.Type) })
	without, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: true})
	require.NoError(t, err)
	assert.Equal(t, recordSteps(full), recordSteps(without))
	assert.Equal(t, full.Totals, without.Totals)
	for _, step := range full.Steps {
		if step.Event != nil {
			assert.False(t, step.Verified)
		} else {
			assert.True(t, step.Verified)
		}
	}
	assert.False(t, full.StatusVerified)
	assert.Empty(t, without.Status)
	assert.Equal(t, full.Final, without.Final)
}

func recordSteps(tr *trajectory.Trajectory) []trajectory.Step {
	return slices.DeleteFunc(slices.Clone(tr.Steps), func(step trajectory.Step) bool { return step.Model == nil && step.Tool == nil })
}

func TestBuildModelProvenance(t *testing.T) {
	rec := treeRecord(t)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	steps := recordSteps(got)
	call := rec.Runs[0].Calls[1]
	step := steps[1].Model
	assert.Equal(t, call.Record.Key, step.Key)
	assert.Equal(t, call.Activity, step.Activity)
	assert.Equal(t, call.Step, step.Step)
	assert.Equal(t, call.Started.Model, step.Model)
	assert.Equal(t, call.Started.ModelID, step.ModelID)
	assert.Equal(t, call.Completion.Usage, step.Usage)
	assert.Equal(t, 12*time.Millisecond, step.Latency)
	assert.Equal(t, call.Completion.Facts, step.Facts)
	assert.Equal(t, call.Started.Request.System, step.SystemHash)
	assert.Equal(t, call.Started.Request.Tools, step.ToolsHash)
	assert.Equal(t, call.Started.Request.Request, step.RequestDigest)
	assert.NotEmpty(t, step.Prompts)
	assert.Equal(t, call.Started.Prompts, step.Prompts)
}

func TestBuildOrdersModelAttempts(t *testing.T) {
	for _, attempts := range [][]int{{2, 1}, {1, 2}} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			rec := treeRecord(t)
			original := rec.Runs[0].Calls[0]
			rec.Runs[0].Calls = nil
			rec.Runs[0].Entries = nil
			rec.Runs[0].Events = nil
			for _, attempt := range attempts {
				call := original
				call.Record.Key = runstore.CallKey{RunID: "run", Seq: 1, Attempt: attempt}
				rec.Runs[0].Calls = append(rec.Runs[0].Calls, call)
			}
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			steps := recordSteps(got)
			require.Len(t, steps, 2)
			assert.Equal(t, []int{1, 1}, []int{steps[0].Model.Key.Seq, steps[1].Model.Key.Seq})
			assert.Equal(t, []int{1, 2}, []int{steps[0].Model.Key.Attempt, steps[1].Model.Key.Attempt})
		})
	}
}

func stepKinds(steps []trajectory.Step) []trajectory.StepKind {
	kinds := make([]trajectory.StepKind, len(steps))
	for i, step := range steps {
		kinds[i] = step.Kind
	}
	return kinds
}
