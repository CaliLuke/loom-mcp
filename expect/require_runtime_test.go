package expect_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeFatalRecorder struct {
	testing.TB
	messages []string
}

func TestRequireRealTrajectoryDiagnostics(t *testing.T) {
	tr := recordedTrajectory(t, runtimeScenario{turns: []agenttest.Turn{toolTurn("echo.echo", `{"value":"good"}`), finalTurn()}})
	recorder := &runtimeFatalRecorder{}
	expectations := []expect.Expectation{expect.Forbid(runtimeEchoTool()), expect.Completed(), expect.MaxModelCalls(1), expect.MaxTokens(9)}
	checks := expect.Evaluate(expect.Trajectory(tr), expectations...)
	assert.PanicsWithValue(t, recorder, func() {
		expect.Require(recorder, expect.Trajectory(tr), expectations...)
	})
	require.Len(t, recorder.messages, 1)
	message := recorder.messages[0]
	first := strings.Index(message, checks[0].Diagnostic)
	second := strings.Index(message, checks[2].Diagnostic)
	third := strings.Index(message, checks[3].Diagnostic)
	assert.GreaterOrEqual(t, first, 0)
	assert.Less(t, first, second)
	assert.Less(t, second, third)
	assert.NotContains(t, message, "completed:")
}

func (r *runtimeFatalRecorder) Helper() {
}
func (r *runtimeFatalRecorder) Fatal(args ...any) {
	r.messages = append(r.messages, fmt.Sprint(args...))
	panic(r)
}
