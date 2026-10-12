package gate

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReportPrecisionAndFinalFail(t *testing.T) {
	e, err := Evaluate(testTable(48), testConfig(), 1)
	require.NoError(t, err)
	e.Variants[0].Success.Status = Fail
	fail, err := Decide(e, nil)
	require.NoError(t, err)
	assert.Equal(t, Fail, fail.Status)
	e.Look = 2
	_, err = Decide(e, []Decision{fail})
	require.Error(t, err)
	e.Variants[0].Success.Status = Pass
	pass, err := Decide(e, []Decision{{Look: 1, Status: Continue}})
	require.NoError(t, err)
	assert.Equal(t, Pass, pass.Status)
	e.Look = 3
	e.Variants[0].Recall.Status = Inconclusive
	d, err := Decide(e, nil)
	require.NoError(t, err)
	report := Render(e, d)
	assert.Contains(t, report, "NOT_PASSED")
	assert.Contains(t, report, "precision, not a measured regression")
	assert.Contains(t, report, "receipt_only/recall: INCONCLUSIVE")
}
