package gate

import (
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStoredRows(t *testing.T) {
	a := eval.StoredAttempt{AttemptKey: eval.AttemptKey{Variant: "hybrid", Scenario: "a", Item: "one", Repeat: 1}, Checks: []expect.Check{{Name: "task_success", Passed: true}, {Name: "no_unsupported_claim", Passed: true}}, Scores: []expect.Score{{Name: "evidence_used", Value: 1}, {Name: "recall_qualifying", Value: 1}, {Name: "recall_success", Value: 1}}, Runs: []eval.StoredRun{{RunError: "agent failed"}}}
	rows, err := FromStored([]eval.StoredAttempt{a})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].RunFailed)
	assert.False(t, rows[0].Infrastructure)
	a.Error = "infrastructure"
	a.Checks = nil
	rows, err = FromStored([]eval.StoredAttempt{a})
	require.NoError(t, err)
	assert.True(t, rows[0].Infrastructure)
	a.Error = ""
	_, err = FromStored([]eval.StoredAttempt{a})
	require.Error(t, err)
}
