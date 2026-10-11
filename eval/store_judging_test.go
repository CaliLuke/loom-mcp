package eval

import (
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
