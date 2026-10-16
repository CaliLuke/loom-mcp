package agenttest_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepeatFailNowIsIsolatedAndRunsCleanup(t *testing.T) {
	tb := &evalTB{TB: t}
	attempts, cleanups := 0, 0
	afterFailure := false
	report := agenttest.Repeat(tb, 2, 1, func(at testing.TB) {
		attempts++
		assert.False(t, at.Failed(), "each attempt starts clean")
		at.Cleanup(func() {
			cleanups++
		})
		if attempts == 1 {
			at.Fail()
			assert.True(t, at.Failed())
			at.FailNow()
			afterFailure = true
		}
	})
	assert.False(t, afterFailure)
	assert.Equal(t, 2, cleanups)
	assert.Equal(t, 2, report.Attempts)
	assert.Equal(t, 1, report.Passed)
	require.Len(t, report.Failed, 1)
	assert.Equal(t, 1, report.Failed[0].Index)
	assert.False(t, tb.failed, "one successful attempt meets the threshold")
}

func TestRepeatFormattedSkipStopsRemainingAttempts(t *testing.T) {
	tb := &evalTB{TB: t}
	attempts := 0
	runTB(func() {
		agenttest.Repeat(tb, 3, 1, func(at testing.TB) {
			attempts++
			at.Skipf("missing %s", "fixture")
		})
	})
	assert.Equal(t, 1, attempts)
	assert.True(t, tb.skipped)
	assert.False(t, tb.failed)
}
