package agenttest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepeatThresholds(t *testing.T) {
	for _, minPass := range []int{3, 4} {
		t.Run(fmt.Sprint(minPass), func(t *testing.T) {
			tb := &evalTB{TB: t}
			attempt := 0
			report := agenttest.Repeat(tb, 5, minPass, func(at testing.TB) {
				attempt++
				assert.Contains(t, at.Name(), fmt.Sprintf("/attempt-%d", attempt))
				at.Log("attempt log")
				if attempt == 2 || attempt == 4 {
					at.Errorf("failure %d", attempt)
				}
			})
			assert.Equal(t, 5, report.Attempts)
			assert.Equal(t, 3, report.Passed)
			require.Len(t, report.Failed, 2)
			assert.Equal(t, 2, report.Failed[0].Index)
			assert.Equal(t, 4, report.Failed[1].Index)
			assert.Equal(t, minPass == 4, tb.failed)
			output := strings.Join(append(tb.logs, tb.diagnostics...), "\n")
			assert.Less(t, strings.Index(output, "failure 2"), strings.Index(output, "failure 4"))
			assert.Contains(t, output, "attempt 1/5:")
		})
	}
}

func TestRepeatFatalPanicAndCleanup(t *testing.T) {
	tb := &evalTB{TB: t}
	var order []string
	attempt := 0
	afterFatal := false
	report := agenttest.Repeat(tb, 3, 1, func(at testing.TB) {
		attempt++
		at.Cleanup(func() {
			assert.Error(t, at.Context().Err())
			order = append(order, fmt.Sprintf("%d-first", attempt))
		})
		at.Cleanup(func() {
			order = append(order, fmt.Sprintf("%d-second", attempt))
			if attempt == 3 {
				at.Error("cleanup failed")
			}
		})
		switch attempt {
		case 1:
			at.Fatal("fatal attempt")
			afterFatal = true
		case 2:
			panic("panic attempt")
		}
	})
	assert.False(t, afterFatal)
	assert.Equal(t, []string{"1-second", "1-first", "2-second", "2-first", "3-second", "3-first"}, order)
	assert.Equal(t, 0, report.Passed)
	require.Len(t, report.Failed, 3)
	assert.Contains(t, strings.Join(report.Failed[1].Diagnostics, "\n"), "panic attempt")
	assert.Contains(t, strings.Join(report.Failed[1].Diagnostics, "\n"), "goroutine")
	assert.Contains(t, strings.Join(report.Failed[2].Diagnostics, "\n"), "cleanup failed")
}

func TestRepeatSkipAndInvalidThreshold(t *testing.T) {
	tb := &evalTB{TB: t}
	attempts := 0
	runTB(func() {
		agenttest.Repeat(tb, 5, 1, func(at testing.TB) {
			attempts++
			at.SkipNow()
		})
	})
	assert.True(t, tb.skipped)
	assert.Equal(t, 1, attempts)
	for _, minPass := range []int{0, 6} {
		tb := &evalTB{TB: t}
		called := false
		runTB(func() {
			agenttest.Repeat(tb, 5, minPass, func(testing.TB) {
				called = true
			})
		})
		assert.True(t, tb.failed)
		assert.False(t, called)
	}
}

func TestRepeatErrorThenSkipFailsAttempt(t *testing.T) {
	for _, minPass := range []int{1, 2} {
		t.Run(map[int]string{1: "threshold_met", 2: "threshold_missed"}[minPass], func(t *testing.T) {
			tb := &evalTB{TB: t}
			attempts := 0
			var report agenttest.RepeatReport
			runTB(func() {
				report = agenttest.Repeat(tb, 2, minPass, func(at testing.TB) {
					attempts++
					if attempts == 1 {
						at.Error("failure before skip")
						at.Skip("skip after failure")
					}
				})
			})
			assert.False(t, tb.skipped)
			assert.Equal(t, minPass == 2, tb.failed)
			assert.Equal(t, 2, report.Attempts)
			assert.Equal(t, 1, report.Passed)
			if assert.Len(t, report.Failed, 1) {
				assert.Equal(t, 1, report.Failed[0].Index)
				assert.Contains(t, strings.Join(report.Failed[0].Diagnostics, "\n"), "failure before skip")
			}
		})
	}
}
