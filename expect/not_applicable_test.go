package expect_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallsNotApplicable(t *testing.T) {
	tool := echoTool()
	unexpected := func() {
		t.Error("run-only predicate invoked on Calls")
	}
	expectations := make([]expect.Expectation, 0, 11)
	expectations = append(expectations,
		expect.Call(tool, nil, func(*echoResult) error {
			unexpected()
			return nil
		}),
		expect.Failed(tool, func(*echoPayload) error {
			unexpected()
			return nil
		}, func(*planner.ToolError) error {
			unexpected()
			return nil
		}),
		expect.Bounds(tool, func(*agent.Bounds) error {
			unexpected()
			return nil
		}),
		expect.Completed(),
		expect.AllAttemptsSuccessful(tool),
		expect.MaxModelCalls(1),
		expect.MaxTokens(100),
	)
	for _, kind := range []planner.AwaitItemKind{planner.AwaitItemKindClarification, planner.AwaitItemKindQuestions, planner.AwaitItemKindExternalTools, planner.AwaitItemKindTypedInput} {
		expectations = append(expectations, expect.Await(kind, func(planner.AwaitItem) error {
			unexpected()
			return nil
		}))
	}
	for _, tc := range []struct {
		name  string
		calls []model.ToolCall
	}{
		{"nil", nil}, {"empty", []model.ToolCall{}}, {"populated", []model.ToolCall{modelCall(tool.Name, "a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := expect.Evaluate(expect.Calls(tc.calls), expectations...)
			require.Len(t, checks, len(expectations))
			for _, check := range checks {
				assert.False(t, check.Passed, check.Name)
				assert.Equal(t, "not applicable to a model-call subject", check.Diagnostic)
			}
		})
	}
}
