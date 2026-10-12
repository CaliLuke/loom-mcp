package suite_test

import (
	"testing"
	"time"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportedMedianForTwoRepeats(t *testing.T) {
	rows := []eval.StoredAttempt{
		{AttemptKey: eval.AttemptKey{Variant: "hybrid", Scenario: "example"}, Duration: 2 * time.Second},
		{AttemptKey: eval.AttemptKey{Variant: "hybrid", Scenario: "example"}, Duration: 4 * time.Second},
	}
	result, err := suite.Describe(rows)
	require.NoError(t, err)
	assert.InDelta(t, 3.0, result["hybrid/example"].MedianSeconds, 1e-12)
	assert.InDelta(t, 4.0, result["hybrid/example"].P90Seconds, 1e-12)
}
