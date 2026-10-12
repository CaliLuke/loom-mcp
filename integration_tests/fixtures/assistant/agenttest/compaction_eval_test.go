package agenttest_test

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/summaryeval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const compactionSummary = "# Goal\nRemember evidence.\n# Decisions\nUse facts.\n# Facts\nTicket 42.\n# Progress\nRead status.\n# Open questions\nNone.\n# Next\nContinue."

func TestCompactionEvalRecordedHarness(t *testing.T) {
	var output, diagnostic bytes.Buffer
	dir := t.TempDir()
	turn := agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{}))
	factory := eval.NewHarnessFunc(guideRegister, func(h *agenttest.Harness, _ eval.Variant) error {
		agenttest.Stub(h, projected.ProjectedStatusToolTool).Return(&projected.ProjectedStatusToolResult{Status: strings.Repeat("Ticket 42 ready. ", 500)}).Times(3)
		return nil
	},
		agenttest.WithDefaultRunOptions(runtime.WithContextOverrides(runtime.ContextOverrides{ContextWindow: new(5000)})),
		agenttest.ScriptedModel("fixture", turn, turn, turn, agenttest.Respond(agenttest.Text("done"))),
		agenttest.ScriptedModel("summary", agenttest.Respond(agenttest.Text(compactionSummary)), agenttest.Respond(agenttest.Text(compactionSummary)), agenttest.Respond(agenttest.Text("headless rejected"))),
		agenttest.WithRuntimeOptions(runtime.WithContextCompaction("assistant.assistant_runtime", runtime.ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30, ProtectRecentToolTokens: 1, MinSavingTokens: 20, ContextModel: &runtime.ContextModel{ID: "summary"}}), runtime.WithContextLimits(map[string]model.ContextLimits{"fixture": {ContextWindow: 200000}, "summary": {ContextWindow: 200000}})))
	cfg := evalcli.Config{StoreDir: dir, Stdout: &output, Stderr: &diagnostic, Harness: factory, Scenarios: []eval.Scenario{{ID: "summary", Expect: []expect.Expectation{expect.Completed()}, Agent: "assistant.assistant_runtime", Items: eval.Items(eval.Item{ID: "one", Input: strings.Repeat("remember ticket 42 ", 500)})}}}
	require.Equal(t, 0, evalcli.Main(t.Context(), []string{"run"}, cfg), diagnostic.String())
	cuts, source, err := summaryeval.Read(dir, summaryeval.Selection{From: "last", MaxCuts: 3})
	require.NoError(t, err)

	require.Len(t, cuts, 3)
	assert.Equal(t, []string{"summary", "summary", "rejected"}, []string{cuts[0].Origin, cuts[1].Origin, cuts[2].Origin})
	for _, cut := range cuts {
		v, err := summaryeval.Verify(t.Context(), cut)
		require.NoError(t, err)
		assert.Equal(t, "verified", v.Status, "%+v", v)
	}
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("v2 one")), agenttest.Respond(agenttest.Text("v2 two")), agenttest.Respond(agenttest.Text("v2 three")))
	require.NoError(t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	guard, err := agenttest.NewScript()
	require.NoError(t, err)
	cfg.Compaction = &summaryeval.Config{Prompts: map[string]string{"v2": "V2 Previous={{.Previous}} Evicted={{.Evicted}} Instructions={{.Instructions}} Budget={{.Budget}}"}, NewModels: func() (map[string]summaryeval.Model, error) {
		return map[string]summaryeval.Model{"summary": {Client: script.Client()}, "fixture": {Client: guard.Client()}}, nil
	}}
	judgeTurns := make([]agenttest.Turn, 0, 10)
	for _, label := range []string{"entailed", "contradicted", "not_addressed", "indeterminate"} {
		b, err := json.Marshal(map[string]any{"arrival": map[string]string{"label": label, "rationale": "calibration"}})
		require.NoError(t, err)
		judgeTurns = append(judgeTurns, agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(b))))
	}
	judgments := map[string]any{}
	for _, f := range summaryeval.Rubric() {
		judgments[f.Claim.ID] = map[string]string{"label": "entailed", "rationale": "evidence retained"}
	}
	payload, err := json.Marshal(judgments)
	require.NoError(t, err)
	for range 6 {
		judgeTurns = append(judgeTurns, agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(payload))))
	}
	judgeScript, err := agenttest.NewScript(judgeTurns...)
	require.NoError(t, err)
	cfg.NewJudge = func(cache judge.Cache) (*judge.Judge, error) {
		return judge.New(judge.Config{Client: judgeScript.Client(), ModelID: "judge", MaxOutputTokens: 2048, Cache: cache})
	}
	output.Reset()
	diagnostic.Reset()
	require.Equal(t, 0, evalcli.Main(t.Context(), []string{"compaction", "run", "--from", source.Experiment, "--variant", "prompt=v2"}, cfg), diagnostic.String())
	id, err := summaryeval.Resolve(dir, "last")
	require.NoError(t, err)
	result, err := summaryeval.Load(dir, id)
	require.NoError(t, err)
	require.Len(t, result.Lines, 3)
	for i, line := range result.Lines {
		assert.Equal(t, "verified", line.Verification.Status)
		assert.Len(t, line.Judgments, 12)
		assert.Equal(t, cuts[i].Old.Text, line.Old.Text)
		assert.Equal(t, []string{"v2 one", "v2 two", "v2 three"}[i], line.New.Text)
	}
	require.Len(t, script.Requests(), 3)
	assert.Contains(t, script.Requests()[1].Messages[0].Parts[0].(model.TextPart).Text, compactionSummary[:6])
	require.NoError(t, script.Err())
	assert.Empty(t, guard.Requests())
	require.NoError(t, guard.Err())
	require.NoError(t, judgeScript.Err())
	store, err := eval.Open(dir)
	require.NoError(t, err)
	stored, err := store.Load(source.Experiment)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "export.json")
	require.NoError(t, os.WriteFile(path, stored.Attempts[0].Runs[0].Export, 0600))
	exported, _, err := summaryeval.Read(dir, summaryeval.Selection{Export: path})
	require.NoError(t, err)
	require.Len(t, exported, len(cuts))
	for i, cut := range cuts {
		assert.Equal(t, cut.Record, exported[i].Record, "cut %d record", i)
		assert.Equal(t, cut.From, exported[i].From, "cut %d from", i)
		assert.Equal(t, cut.To, exported[i].To, "cut %d to", i)
		assert.Equal(t, cut.Old, exported[i].Old, "cut %d old", i)
	}
	selected, _, err := summaryeval.Read(dir, summaryeval.Selection{From: "last", Attempts: []string{"#0"}, Step: cuts[1].Step, MaxCuts: 1})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, cuts[1].Record, selected[0].Record)
}
