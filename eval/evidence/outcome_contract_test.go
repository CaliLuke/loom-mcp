package evidence

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifiedEvidenceExportsIndependentChecksAndOperationCounts(t *testing.T) {
	rec := record(call(1, msg("NEED: ABC-12345")))
	rec.Runs[0].Transcript = append(rec.Runs[0].Transcript, answer("ABC-12345"))
	rec.Runs[0].Summary.Rejections = 3
	rec.Runs[0].Events = []*runlog.Event{nil,
		{Type: hooks.ContextCompacted}, {Type: hooks.ContextCompacted}, {Type: hooks.ContextOverflow},
		{Type: hooks.ContextRecalled, Payload: []byte(`{"outcome":"found"}`)},
		{Type: hooks.ContextRecalled, Payload: []byte(`{"outcome":"exhausted"}`)},
	}
	result, err := Classify([]*runrecord.RunRecord{rec}, spec(), true)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{
		"evidence_used": 1, "evidence_absent": 0, "need_found": 1, "recall_qualifying": 0, "recall_success": 0, "recall_exhausted": 1,
		"compactions": 2, "summary_rejections": 3, "overflow_recoveries": 1, "recall_calls": 2,
	}, result.Scores())
	for _, tc := range []struct{ runs, action, success bool }{
		{true, true, true}, {false, true, false}, {true, false, false}, {false, false, false},
	} {
		assert.Equal(t, map[string]bool{"task_success": tc.success, "no_unsupported_claim": true}, result.Checks(tc.runs, tc.action))
	}
	rec.Runs[0].Transcript = append(rec.Runs[0].Transcript, answer("ABC-12345 XYZ-00000"))
	unsupported, err := Classify([]*runrecord.RunRecord{rec}, spec(), true)
	require.NoError(t, err)
	assert.False(t, unsupported.Checks(true, true)["no_unsupported_claim"])
	rec.Runs[0].Events = []*runlog.Event{{Type: hooks.ContextRecalled, Payload: []byte(`{`)}}
	_, err = Classify([]*runrecord.RunRecord{rec}, spec(), true)
	require.Error(t, err, "malformed canonical events cannot silently count as successful recall")
}
