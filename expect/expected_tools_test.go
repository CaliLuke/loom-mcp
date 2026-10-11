package expect_test

import (
	"encoding/json"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpectedToolsJSONSubset(t *testing.T) {
	for _, tc := range []struct {
		name, expected, actual string
		passed                 bool
	}{
		{"nested object subset", `{"a":{"b":1}}`, `{"a":{"b":1,"c":2},"extra":3}`, true},
		{"missing key", `{"a":null}`, `{}`, false},
		{"null present", `{"a":null}`, `{"a":null,"b":1}`, true},
		{"null mismatch", `null`, `"null"`, false},
		{"array exact", `[1,2]`, `[1.0,2e0]`, true},
		{"array extra", `[1]`, `[1,2]`, false},
		{"array order", `[1,2]`, `[2,1]`, false},
		{"object inside array exact", `[{"a":1}]`, `[{"a":1,"b":2}]`, false},
		{"integer above float precision", `9007199254740993`, `9007199254740992`, false},
		{"large integer same", `9007199254740993`, `9007199254740993.0`, true},
		{"decimal exponent", `0.0012300`, `123e-5`, true},
		{"decimal distinct", `0.10000000000000001`, `0.1`, false},
		{"signed zero", `-0e10`, `0.0`, true},
		{"string and number", `"1"`, `1`, false},
		{"boolean", `true`, `false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := recordedTrajectory(t, runtimeScenario{turns: []agenttest.Turn{toolTurn("echo.echo", `{"data":`+tc.actual+`}`), finalTurn()}})
			expected := expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "echo.echo", Args: json.RawMessage(`{"data":` + tc.expected + `}`)}}}
			check, score := expected.Evaluate(expect.Trajectory(tr))
			assert.Equal(t, tc.passed, check.Passed, check.Diagnostic)
			assert.Equal(t, "tool_correctness", score.Name)
			assert.Nil(t, score.Threshold)
			want := 0.0
			if tc.passed {
				want = 1
			}
			assert.InDelta(t, want, score.Value, 1e-12)
			checks := expect.Evaluate(expect.Trajectory(tr), expected)
			require.Len(t, checks, 1)
			assert.Equal(t, check, checks[0])
		})
	}
}

func TestExpectedToolsOrderAndScores(t *testing.T) {
	a := expect.ExpectedTool{Name: "echo.echo", Args: json.RawMessage(`{"value":"a"}`)}
	b := expect.ExpectedTool{Name: "echo.echo", Args: json.RawMessage(`{"value":"b"}`)}
	for _, tc := range []struct {
		name     string
		actual   []string
		expected expect.ExpectedTools
		passed   bool
		score    float64
	}{
		{"unordered", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{b, a}}, true, 1},
		{"ordered reverse", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{b, a}, Ordered: true}, false, .5},
		{"ordered exact", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{a, b}, Ordered: true, Exact: true}, true, 1},
		{"extra permitted", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{a}}, true, .5},
		{"extra exact", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{a}, Exact: true}, false, .5},
		{"partial", []string{"a"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{a, b}}, false, 1},
		{"duplicate expected cannot reuse", []string{"a"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{a, a}}, false, 1},
		{"broad and specific", []string{"a", "b"}, expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: tools.Ident("echo.echo")}, a}}, true, 1},
		{"no calls none expected", nil, expect.ExpectedTools{}, true, 1},
		{"no calls expected", nil, expect.ExpectedTools{Tools: []expect.ExpectedTool{a}}, false, 0},
		{"calls none expected", []string{"a"}, expect.ExpectedTools{}, true, 0},
		{"calls none expected exact", []string{"a"}, expect.ExpectedTools{Exact: true}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turns := make([]agenttest.Turn, 0, len(tc.actual)+1)
			for _, value := range tc.actual {
				turns = append(turns, toolTurn("echo.echo", `{"value":"`+value+`"}`))
			}
			turns = append(turns, finalTurn())
			tr := recordedTrajectory(t, runtimeScenario{turns: turns})
			check, score := tc.expected.Evaluate(expect.Trajectory(tr))
			assert.Equal(t, tc.passed, check.Passed, check.Diagnostic)
			assert.InDelta(t, tc.score, score.Value, 1e-12)
			if check.Passed {
				assert.Empty(t, check.Diagnostic)
			} else {
				assert.NotEmpty(t, check.Diagnostic)
			}
		})
	}
}
