package eval_test

import (
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
)

func TestVerdict(t *testing.T) {
	threshold := 0.5
	failed := []expect.Check{{Name: "check", Diagnostic: "wrong"}}
	gating := []eval.Claim{{ID: "correct", Text: "Correct"}}
	for _, tc := range []struct {
		name string
		out  eval.Outcome
		want eval.Verdict
	}{
		{"passed", eval.Outcome{}, "passed"},
		{"error precedence", eval.Outcome{Err: errors.New("broken"), Checks: failed, Claims: gating}, "error"},
		{"check", eval.Outcome{Checks: failed}, "failed"},
		{"threshold", eval.Outcome{Scores: []expect.Score{{Value: 0.4, Threshold: &threshold}}}, "failed"},
		{"equal threshold", eval.Outcome{Scores: []expect.Score{{Value: 0.5, Threshold: &threshold}}}, "passed"},
		{"no threshold", eval.Outcome{Scores: []expect.Score{{Value: 0}}}, "passed"},
		{"gating", eval.Outcome{Claims: gating}, "pending"},
		{"informational", eval.Outcome{Claims: []eval.Claim{{Informational: true}}}, "passed"},
		{"failed beats pending", eval.Outcome{Checks: failed, Claims: gating}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.out.Verdict())
		})
	}
	assert.Equal(t, eval.Verdict("error"), (&eval.AttemptRecord{Outcome: &eval.Outcome{}, HarnessErr: errors.New("factory")}).Verdict())
	assert.Equal(t, eval.Verdict("not_run"), (&eval.AttemptRecord{NotRun: "canceled", HarnessErr: errors.New("factory")}).Verdict())
	var record *eval.AttemptRecord
	assert.Nil(t, record.Runs())
}
