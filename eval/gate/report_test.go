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
	assert.Equal(t, Complete, fail.Status)
	assert.Equal(t, Fail, fail.Variants[0].Status)
	e.Look = 2
	_, err = Decide(e, []Decision{fail})
	require.Error(t, err)
	e.Variants[0].Success.Status = Pass
	first := e
	first.Look = 1
	first.Variants = append([]VariantResult(nil), e.Variants...)
	first.Variants[0].Success.Status = Inconclusive
	first.Variants[1].Success.Status = Inconclusive
	prefix, err := Decide(first, nil)
	require.NoError(t, err)
	pass, err := Decide(e, []Decision{prefix})
	require.NoError(t, err)
	assert.Equal(t, Complete, pass.Status)
	for _, v := range pass.Variants {
		assert.Equal(t, Pass, v.Status)
	}
	e.Look = 3
	e.Variants[0].Recall.Status = Inconclusive
	d, err := DecideMode(e, nil, Offline)
	require.NoError(t, err)
	report := Render(e, d)
	assert.Contains(t, report, "NOT_PASSED")
	assert.Contains(t, report, "precision, not a measured regression")
	assert.Contains(t, report, "receipt_only/recall: INCONCLUSIVE")
}
