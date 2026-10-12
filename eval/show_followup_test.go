package eval

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShowOrphanJudgments(t *testing.T) {
	a := &StoredAttempt{
		Claims: []StoredClaim{{ID: "known", Text: "known claim"}},
		Judgments: []StoredJudgment{
			{ClaimID: "known", Label: "entailed", Rationale: "matched"},
			{ClaimID: "missing", Subject: "tool_args", Error: "missing subject", Uncalibrated: true, Truncated: true, Step: &StoredJudgedStep{Run: 1, Index: 2, Tool: "lookup", CallID: "call"}},
		},
	}
	var out bytes.Buffer
	require.NoError(t, Show(&out, &Experiment{}, a, ShowOptions{}))
	assert.Contains(t, out.String(), "Orphan judgments:\n    [error] missing (tool_args step r1s2 lookup call) uncalibrated truncated: missing subject")
	assert.Contains(t, out.String(), "[entailed] known (answer): matched")
	assert.Equal(t, 1, bytes.Count(out.Bytes(), []byte("[entailed] known")))
}
