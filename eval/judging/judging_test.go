package judging_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/judging"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubjectBatchesAndOrder(t *testing.T) {
	client, script := agenttest.ScriptClient(t, turn("answer"), turn("trajectory"), turn("session"), turn("step"), turn("step"))
	j := adapter(t, client)
	out := outcome()
	out.Claims = []eval.Claim{{ID: "step", Text: "step", Subject: eval.SubjectStep, Tool: "search"}, {ID: "session", Text: "session", Subject: eval.SubjectSession}, {ID: "answer", Text: "answer", Subject: eval.SubjectAnswer}, {ID: "trajectory", Text: "trajectory", Subject: eval.SubjectTrajectory}}
	before, err := out.Session.Trajectory.Marshal()
	require.NoError(t, err)
	result, err := j.JudgeOutcome(t.Context(), out)
	require.NoError(t, err)
	require.Len(t, result.Judgments, 5)
	assert.Equal(t, []string{"step", "step", "session", "answer", "trajectory"}, claimIDs(result.Judgments))
	assert.Equal(t, &eval.JudgedStep{Run: 1, Index: 1, Tool: "search", CallID: "one"}, result.Judgments[0].Step)
	assert.Equal(t, &eval.JudgedStep{Run: 1, Index: 3, Tool: "search", CallID: "two"}, result.Judgments[1].Step)
	reqs := script.Requests()
	require.Len(t, reqs, 5)
	opts := trajectory.RenderOptions{Limits: trajectory.DefaultLimits()}
	texts := []string{"first\nsecond", out.Session.Trajectory[1].Render(opts), out.Session.Trajectory.Render(opts)}
	subjects := []string{"answer", "trajectory", "session", "step", "step"}
	for i, req := range reqs {
		text := requestText(req)
		assert.Contains(t, text, "Subject kind: "+subjects[i])
		assert.Contains(t, text, "Task:\nhelp")
		assert.Contains(t, text, "Reference:\nfacts")
		assert.Contains(t, text, "Claims:\n"+subjects[i]+": "+subjects[i]+"\n")
		if i < 3 {
			assert.Contains(t, text, "Subject:\n`\n"+texts[i]+"\n`")
		}
	}
	first := requestText(reqs[3])
	assert.Contains(t, first, "prior answer")
	assert.Contains(t, first, "Judged call: run 2 step 1 search one")
	assert.Contains(t, first, "Result: null")
	assert.NotContains(t, first, "secret result")
	assert.NotContains(t, first, "later arguments")
	assert.NotContains(t, first, "Final answer:\n  first")
	after, err := out.Session.Trajectory.Marshal()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, 15, result.Tokens)
	assert.Equal(t, 10, result.Usage.InputTokens)
	assert.Equal(t, 5, result.Usage.OutputTokens)
}

func TestAnswerNoCallAndParts(t *testing.T) {
	client, script := agenttest.ScriptClient(t, turn("a"))
	j := adapter(t, client)
	out := outcome()
	out.Claims = []eval.Claim{{ID: "a", Text: "answer", Subject: eval.SubjectAnswer}}
	out.Session.Trajectory[1].Final = nil
	result, err := j.JudgeOutcome(t.Context(), out)
	require.NoError(t, err)
	assert.Equal(t, eval.NotAddressed, result.Judgments[0].Label)
	assert.False(t, result.Judgments[0].Uncalibrated)
	assert.Empty(t, script.Requests())
	out.Session.Trajectory[1].Final = &model.Message{Parts: []model.Part{&model.TextPart{Text: "text"}, model.ImagePart{}, model.ThinkingPart{}, model.DocumentPart{}, model.CitationsPart{}, model.ToolUsePart{}, model.ToolResultPart{}, model.CacheCheckpointPart{}}}
	result, err = j.JudgeOutcome(t.Context(), out)
	require.NoError(t, err)
	assert.Equal(t, eval.Entailed, result.Judgments[0].Label)
	assert.Contains(t, requestText(script.Requests()[0]), "text\n[image part]\n[thinking part]\n[document part]\n[citations part]\n[tool_use part]\n[tool_result part]\n[cache_checkpoint part]")
}

func TestTruncation(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "small", true: "large"}[large], func(t *testing.T) {
			client, script := agenttest.ScriptClient(t, turn("a", "b"))
			j := adapter(t, client)
			out := outcome()
			out.Claims = []eval.Claim{{ID: "a", Text: "a", Subject: eval.SubjectTrajectory}, {ID: "b", Text: "b", Subject: eval.SubjectTrajectory}}
			value := "[truncated: marker text]"
			if large {
				value = strings.Repeat("x", 10*1024)
			}
			out.Session.Trajectory[1].Steps[1].Tool.Result = rawjson.Message(value)
			result, err := j.JudgeOutcome(t.Context(), out)
			require.NoError(t, err)
			assert.Equal(t, large, result.Judgments[0].Truncated)
			assert.Equal(t, large, result.Judgments[1].Truncated)
			assert.Equal(t, large, strings.Contains(requestText(script.Requests()[0]), "TRUNCATED: the renderer elided content"))
		})
	}
}

func TestStepSelectorsAndMissingUnits(t *testing.T) {
	client, script := agenttest.ScriptClient(t, turn("model_a", "model_b"), turn("model_a", "model_b"), turn("tool"), turn("tool"))
	j := adapter(t, client)
	out := outcome()
	out.Session.Trajectory[1].Steps[1].Tool.Children = []*trajectory.Trajectory{{RunID: "child", Steps: []trajectory.Step{{Kind: trajectory.StepTool, Tool: &trajectory.ToolStep{Name: "search", ToolCallID: "child-call"}}}}}
	out.Session.Trajectory[1].Steps = append(out.Session.Trajectory[1].Steps, trajectory.Step{Kind: trajectory.StepModel, Model: &trajectory.ModelStep{Class: runrecord.Abandoned, Chosen: []model.ToolCall{{Name: "ignored"}}}})
	out.Claims = []eval.Claim{{ID: "model_a", Text: "a", Subject: eval.SubjectStep}, {ID: "tool", Text: "tool", Subject: eval.SubjectStep, Tool: "search"}, {ID: "model_b", Text: "b", Subject: eval.SubjectStep}, {ID: "missing", Text: "missing", Subject: eval.SubjectStep, Tool: "absent"}}
	result, err := j.JudgeOutcome(t.Context(), out)
	require.NoError(t, err)
	assert.Equal(t, []string{"model_a", "model_a", "tool", "tool", "model_b", "model_b", "missing"}, claimIDs(result.Judgments))
	require.Len(t, script.Requests(), 4)
	assert.Equal(t, 0, result.Judgments[0].Step.Index)
	assert.Equal(t, 2, result.Judgments[1].Step.Index)
	missing := result.Judgments[6]
	assert.Equal(t, eval.NotAddressed, missing.Label)
	assert.Equal(t, "no matching step", missing.Rationale)
	assert.Nil(t, missing.Step)
}

func TestBatchErrorsContinue(t *testing.T) {
	failure := errors.New("provider offline")
	client, script := agenttest.ScriptClient(t, agenttest.Fail(failure), turn("trajectory"))
	j := adapter(t, client)
	out := outcome()
	out.Claims = []eval.Claim{{ID: "answer", Text: "a", Subject: eval.SubjectAnswer}, {ID: "trajectory", Text: "t", Subject: eval.SubjectTrajectory}, {ID: "session", Text: "s", Subject: eval.SubjectSession}}
	out.Session.Trajectory[0] = nil
	result, err := j.JudgeOutcome(t.Context(), out)
	require.NoError(t, err)
	require.ErrorIs(t, result.Judgments[0].Err, failure)
	assert.Equal(t, eval.Entailed, result.Judgments[1].Label)
	require.ErrorContains(t, result.Judgments[2].Err, "render")
	assert.Len(t, script.Requests(), 2)
}

func TestCalibrationFlags(t *testing.T) {
	for _, passed := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "passed"}[passed], func(t *testing.T) {
			labels := []judge.Label{judge.Entailed, judge.Contradicted, judge.NotAddressed, judge.Indeterminate}
			if !passed {
				labels[0] = judge.Contradicted
			}
			turns := make([]agenttest.Turn, 0, 5)
			for _, label := range labels {
				turns = append(turns, agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(`{"arrival":{"label":"`+string(label)+`","rationale":"evidence"}}`))))
			}
			turns = append(turns, turn("a"))
			client, script := agenttest.ScriptClient(t, turns...)
			base, err := judge.New(judge.Config{Client: client, ModelID: "scripted", MaxOutputTokens: 512, Cache: judge.NewMemoryCache()})
			require.NoError(t, err)
			j, err := judging.New(judging.Config{Judge: base})
			require.NoError(t, err)
			require.NoError(t, j.Prepare(t.Context()))
			out := outcome()
			out.Claims = []eval.Claim{{ID: "a", Text: "a", Subject: eval.SubjectAnswer}}
			result, err := j.JudgeOutcome(t.Context(), out)
			require.NoError(t, err)
			assert.Equal(t, passed, j.Info().Calibrated)
			assert.Equal(t, !passed, result.Judgments[0].Uncalibrated)
			assert.Len(t, script.Requests(), 5)
		})
	}
}

func adapter(t *testing.T, client model.Client) *judging.Judging {
	t.Helper()
	cache := judge.NewMemoryCache()
	base, err := judge.New(judge.Config{Client: client, ModelID: "scripted", MaxOutputTokens: 512, Cache: cache})
	require.NoError(t, err)
	require.NoError(t, cache.Store(&judge.Calibration{Key: base.CalibrationKey(), Passed: true, Completed: time.Now().UTC()}))
	j, err := judging.New(judging.Config{Judge: base})
	require.NoError(t, err)
	require.NoError(t, j.Prepare(t.Context()))
	return j
}

func turn(ids ...string) agenttest.Turn {
	payload := make(map[string]map[string]string, len(ids))
	for _, id := range ids {
		payload[id] = map[string]string{"label": "entailed", "rationale": "evidence"}
	}
	data, err := callrecord.Encode(payload)
	if err != nil {
		panic(err)
	}
	return agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(data)), agenttest.Usage(2, 1))
}

func requestText(req *model.Request) string {
	return req.Messages[1].Parts[0].(model.TextPart).Text
}
func claimIDs(js []eval.Judgment) []string {
	ids := make([]string, len(js))
	for i, j := range js {
		ids[i] = j.ClaimID
	}
	return ids
}

func outcome() *eval.Outcome {
	prior := &trajectory.Trajectory{RunID: "prior", Final: &model.Message{Parts: []model.Part{model.TextPart{Text: "prior answer"}}}}
	last := &trajectory.Trajectory{RunID: "last", Final: &model.Message{Parts: []model.Part{model.TextPart{Text: "first"}, model.TextPart{Text: "second"}}}, Steps: []trajectory.Step{
		{Kind: trajectory.StepModel, Model: &trajectory.ModelStep{Class: runrecord.Adopted, Chosen: []model.ToolCall{{Name: "search", ID: "one", Payload: rawjson.Message(`{"q":"first"}`)}}}},
		{Kind: trajectory.StepTool, Tool: &trajectory.ToolStep{Name: "search", ToolCallID: "one", Arguments: rawjson.Message(`{"q":"first"}`), Result: rawjson.Message(`"secret result"`)}},
		{Kind: trajectory.StepModel, Model: &trajectory.ModelStep{Class: runrecord.Adopted, Chosen: []model.ToolCall{{Name: "search", ID: "two", Payload: rawjson.Message(`{"q":"later arguments"}`)}}}},
		{Kind: trajectory.StepTool, Tool: &trajectory.ToolStep{Name: "search", ToolCallID: "two", Arguments: rawjson.Message(`{"q":"later arguments"}`)}},
	}}
	return &eval.Outcome{Task: "help", Reference: "facts", Session: &agenttest.SessionResult{Trajectory: trajectory.Session{prior, last}}}
}

func TestJudgingInvalidArguments(t *testing.T) {
	_, err := judging.New(judging.Config{})
	require.Error(t, err)
	client, script := agenttest.ScriptClient(t)
	j := adapter(t, client)
	_, err = j.JudgeOutcome(t.Context(), nil)
	require.Error(t, err)
	out := outcome()
	out.Claims = []eval.Claim{{ID: "a", Text: "a", Subject: "unknown"}}
	_, err = j.JudgeOutcome(t.Context(), out)
	require.ErrorContains(t, err, "subject")
	assert.Empty(t, script.Requests())
}
