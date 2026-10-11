package eval

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC4–5/X1: reserved judging members survive decoding and canonical encoding.
func TestStoreJudgingGolden(t *testing.T) {
	data, err := readStoreTestFile("testdata/store/attempt_judged.golden.jsonl")
	require.NoError(t, err)
	a, err := decodeAttemptLine(data)
	require.NoError(t, err)
	assert.Equal(t, []StoredClaim{{ID: "helpful", Text: "Helpful", Informational: true, Subject: "step", Tool: "assistant.lookup"}}, a.Claims)
	require.Len(t, a.Judgments, 1)
	assert.Equal(t, "step", a.Judgments[0].Subject)
	assert.Equal(t, &StoredJudgedStep{Run: 0, Index: 2, Tool: "assistant.lookup", CallID: "call-1"}, a.Judgments[0].Step)
	assert.Equal(t, model.TokenUsage{CacheReadTokens: 1, CacheWriteTokens: 2, InputTokens: 3, OutputTokens: 4, TotalTokens: 7, Model: "judge-model", ModelClass: "default"}, a.JudgeUsage)
	assert.Equal(t, 7, a.JudgeTokens)
	encoded, err := encodeStore(a)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(encoded)+"\n")
}

// X1: new optional members can be absent even when judgments are present.
func TestStoreJudgingOptionalMembers(t *testing.T) {
	data, err := readStoreTestFile("testdata/store/attempt.golden.jsonl")
	require.NoError(t, err)
	a, err := decodeAttemptLine(data)
	require.NoError(t, err)
	a.Judgments = []StoredJudgment{{ClaimID: "helpful", Label: "entailed"}}
	encoded, err := encodeStore(a)
	require.NoError(t, err)
	for _, member := range []string{"subject", "tool", "step", "judge_usage", "judge_tokens"} {
		assert.NotContains(t, string(encoded), `"`+member+`":`)
	}
	decoded, err := decodeAttemptLine(encoded)
	require.NoError(t, err)
	assert.Equal(t, a, decoded)
	assert.Contains(t, string(encoded), `"judgments":[{`)
}

// T17 AC12: actual runner-domain values are mapped by Append, not just decoded.
func TestStoreJudgmentMapping(t *testing.T) {
	cfg := storeConfig()
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	report := storeReport(t, cfg)
	a := report.Attempts[0]
	a.JudgeUsage = model.TokenUsage{InputTokens: 3, OutputTokens: 4}
	a.JudgeTokens = 7
	a.Outcome.Claims = []Claim{{ID: "helpful", Text: "Helpful", Subject: SubjectStep, Tool: "assistant.lookup", Informational: true}}
	a.Outcome.Judgments = []Judgment{{ClaimID: "helpful", Subject: SubjectStep, Step: &JudgedStep{Run: 0, Index: 2, Tool: "assistant.lookup", CallID: "call-1"}, Informational: true, Uncalibrated: true, Truncated: true, Err: context.DeadlineExceeded}}
	require.NoError(t, w.Append(a))
	info := JudgeInfo{ModelID: "script", PromptHash: "prompt", CalibrationKey: "key"}
	w.SetJudge(info)
	require.NoError(t, w.Finish(report, nil))
	loaded, err := s.Load(w.ID())
	require.NoError(t, err)
	assert.Equal(t, &info, loaded.Header.Judge)
	got := loaded.Attempts[0]
	assert.Equal(t, a.JudgeUsage, got.JudgeUsage)
	assert.Equal(t, 7, got.JudgeTokens)
	assert.Equal(t, []StoredClaim{{ID: "helpful", Text: "Helpful", Subject: "step", Tool: "assistant.lookup", Informational: true}}, got.Claims)
	assert.Equal(t, []StoredJudgment{{ClaimID: "helpful", Subject: "step", Step: &StoredJudgedStep{Run: 0, Index: 2, Tool: "assistant.lookup", CallID: "call-1"}, Informational: true, Uncalibrated: true, Truncated: true, Error: context.DeadlineExceeded.Error()}}, got.Judgments)
}
