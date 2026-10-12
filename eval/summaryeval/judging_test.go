package summaryeval

import (
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgedComparison(t *testing.T) {
	cut := Cuts(recordedFixture(t))[0]
	claims := Rubric()
	require.Len(t, claims, 6)
	turns := []agenttest.Turn{judgmentTurn(t, claims, false), judgmentTurn(t, claims, true), judgmentTurn(t, claims, true)}
	script, err := agenttest.NewScript(turns...)
	require.NoError(t, err)
	j := cachedJudge(t, script)
	summaries, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("new fact"), agenttest.Usage(2, 1)), agenttest.Respond(agenttest.Text("new fact"), agenttest.Usage(2, 1)))
	require.NoError(t, err)
	summaries.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return map[string]Model{"summary": {Client: summaries.Client()}}, nil
	}}
	v, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	result, err := Run(t.Context(), cfg, Options{Dir: t.TempDir(), Cuts: []Cut{cut}, Variants: []Variant{v}, Repeat: 2, Judge: j})
	require.NoError(t, err)
	assert.Len(t, script.Requests(), 3)
	before, err := encode(script.Requests()[0])
	require.NoError(t, err)
	after, err := encode(script.Requests()[1])
	require.NoError(t, err)
	assert.Equal(t, strings.ReplaceAll(string(before), "old fact", "new fact"), string(after))
	assert.True(t, result.Header.Judge.Calibrated)
	require.Len(t, result.Lines[0].Judgments, 12)
	aggregates := Aggregate(result.Lines)
	require.Len(t, aggregates, 1)
	assert.InDelta(t, 0.5, aggregates[0].Old.Retention, 0.0001)
	assert.InDelta(t, 1.0, aggregates[0].New.Retention, 0.0001)
	assert.Equal(t, 6, aggregates[0].Better)
	assert.Equal(t, 6, aggregates[0].Same)
	assert.InDelta(t, 1.0, aggregates[0].New.Categories["faithful"].Retention, 0.0001)
	for _, req := range script.Requests() {
		assert.Contains(t, messageText(req.Messages[1]), "original fact")
	}
	assert.Equal(t, 36, result.Header.Tokens) // two summary calls (3) + three judge calls (10)
	require.NoError(t, script.Err())
	result.Lines[0].Verification.Status = "mismatch"
	result.Lines[1].Verification.Status = "unverifiable"
	assert.Empty(t, Aggregate(result.Lines))
}

func TestJudgedRunLargeEvictedResult(t *testing.T) {
	cut := Cuts(recordedFixtureWith(t, strings.Repeat("evidence ", 600000), nil))[0]
	require.Greater(t, cut.EvictedBytes, 5_000_000)
	in, err := Reconstruct(cut)
	require.NoError(t, err)
	ev, err := evidence(cut, in)
	require.NoError(t, err)
	require.Len(t, ev.Units, 1)
	require.True(t, ev.Units[0].IsResult)
	require.Greater(t, len(ev.Units[0].Text), 5_000_000)
	script, err := agenttest.NewScript(judgmentTurn(t, Rubric(), true), judgmentTurn(t, Rubric(), true))
	require.NoError(t, err)
	summaries, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("new fact")))
	require.NoError(t, err)
	summaries.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return map[string]Model{"summary": {Client: summaries.Client()}}, nil
	}}
	variant, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	dir := t.TempDir()
	result, err := Run(t.Context(), cfg, Options{Dir: dir, Cuts: []Cut{cut}, Variants: []Variant{variant}, Judge: cachedJudge(t, script)})
	require.NoError(t, err)
	stored, err := Load(dir, result.Header.ID)
	require.NoError(t, err)
	require.Len(t, stored.Lines, 1)
	line := stored.Lines[0]
	assert.Equal(t, "verified", line.Verification.Status)
	assert.Empty(t, line.Error)
	require.Len(t, line.Judgments, 12)
	for _, judgment := range line.Judgments {
		assert.True(t, judgment.Truncated, "%s/%s", judgment.Side, judgment.ClaimID)
	}
	require.Len(t, script.Requests(), 2)
	for _, request := range script.Requests() {
		assert.Contains(t, messageText(request.Messages[1]), "elided")
	}
	require.NoError(t, script.Err())
	require.NoError(t, summaries.Err())
}

func TestReferenceBoundAndEmptySide(t *testing.T) {
	ev := summarize.Evidence{Previous: "prior", Units: []summarize.Unit{{Label: "tool result message 1", IsResult: true, Text: strings.Repeat("evidence ", 600000)}}}
	reference, truncated := boundedReference(ev, 384<<10)
	assert.True(t, truncated)
	assert.LessOrEqual(t, len(reference), 384<<10)
	assert.Contains(t, reference, "elided")
	script, err := agenttest.NewScript(judgmentTurn(t, Rubric(), true))
	require.NoError(t, err)
	j := cachedJudge(t, script)
	input := judge.Input{Subject: "summary", Task: comparisonTask, Reference: reference, Claims: claimList(Rubric()), Truncated: truncated}
	old, err := judgeSide(t.Context(), j, input, "", "old", Rubric())
	require.NoError(t, err)
	assert.Empty(t, script.Requests())
	for _, claim := range old.judgments {
		assert.True(t, claim.NoCall)
		assert.Equal(t, "not_addressed", claim.Label)
		assert.True(t, claim.Truncated)
	}
	new, err := judgeSide(t.Context(), j, input, "summary", "new", Rubric())
	require.NoError(t, err)
	for _, claim := range new.judgments {
		assert.True(t, claim.Truncated)
	}
	assert.Len(t, script.Requests(), 1)
}

func cachedJudge(t *testing.T, script *agenttest.Script) *judge.Judge {
	t.Helper()
	cache := judge.NewMemoryCache()
	j, err := judge.New(judge.Config{Client: script.Client(), ModelID: "judge", MaxOutputTokens: 2048, Cache: cache})
	require.NoError(t, err)
	require.NoError(t, cache.Store(&judge.Calibration{Key: j.CalibrationKey(), Passed: true, Completed: time.Unix(1, 0)}))
	return j
}

func judgmentTurn(t *testing.T, facts []Fact, all bool) agenttest.Turn {
	t.Helper()
	rows := make(map[string]map[string]string)
	for i, f := range facts {
		label := "entailed"
		if !all && i%2 == 1 {
			label = "not_addressed"
		}
		rows[f.Claim.ID] = map[string]string{"label": label, "rationale": "recorded evidence"}
	}
	b, err := encode(rows)
	require.NoError(t, err)
	return agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(b)), agenttest.Usage(6, 4))
}
