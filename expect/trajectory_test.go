package expect_test

import (
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrajectoryCallsAndResults(t *testing.T) {
	tool := runtimeEchoTool()
	tr := recordedTrajectory(t, runtimeScenario{turns: []agenttest.Turn{toolTurn(tool.Name, `{"value":"fail"}`), toolTurn(tool.Name, `{"value":"good"}`), finalTurn()}})
	resultPredicate := func(r *echoResult) error {
		if r.Value != "good!" {
			return errors.New("wrong result")
		}
		return nil
	}
	errorPredicate := func(err *planner.ToolError) error {
		assert.Contains(t, err.Message, "boom")
		return nil
	}
	for _, tc := range []struct {
		name        string
		expectation expect.Expectation
		passed      bool
		diagnostic  string
	}{
		{"result", expect.Call(tool, nil, resultPredicate), true, ""},
		{"result predicate failure", expect.Call(tool, nil, func(*echoResult) error {
			return errors.New("reject result")
		}), false, "reject result"},
		{"failed", expect.Failed(tool, nil, errorPredicate), true, ""},
		{"failed nil predicates", expect.Failed(tool, nil, nil), true, ""},
		{"successful is not failed", expect.Failed(tool, func(p *echoPayload) error {
			if p.Value != "good" {
				return errors.New("not good")
			}
			return nil
		}, nil), false, "not good"},
		{"error predicate rejects", expect.Failed(tool, nil, func(*planner.ToolError) error {
			return errors.New("reject error")
		}), false, "reject error"},
		{"bounds", expect.Bounds(tool, func(b *agent.Bounds) error {
			assert.Equal(t, 1, b.Returned)
			assert.True(t, b.Truncated)
			return nil
		}), true, ""},
		{"bounds rejects", expect.Bounds(tool, func(*agent.Bounds) error {
			return errors.New("reject bounds")
		}), false, "reject bounds"},
		{"all attempts", expect.AllAttemptsSuccessful(tool), false, "boom"},
		{"completed", expect.Completed(), true, ""},
		{"model budget equal", expect.MaxModelCalls(3), true, ""},
		{"model budget below", expect.MaxModelCalls(2), false, "3"},
		{"tokens equal", expect.MaxTokens(15), true, ""},
		{"tokens below", expect.MaxTokens(14), false, "15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := expect.Evaluate(expect.Trajectory(tr), tc.expectation)
			require.Len(t, checks, 1)
			assert.Equal(t, tc.passed, checks[0].Passed, checks[0].Diagnostic)
			assert.Contains(t, checks[0].Diagnostic, tc.diagnostic)
		})
	}
	codecFailure := tool
	codecFailure.Result.FromJSON = func([]byte) (*echoResult, error) {
		return nil, errors.New("result codec error")
	}
	checks := expect.Evaluate(expect.Trajectory(tr), expect.Call(codecFailure, nil, resultPredicate))
	assert.Contains(t, checks[0].Diagnostic, `"result codec error"`)
}

func TestTrajectoryChildrenAndSession(t *testing.T) {
	tool := runtimeEchoTool()
	tr := recordedTrajectory(t, runtimeScenario{child: true, turns: []agenttest.Turn{toolTurn("child.run", `{}`), toolTurn(tool.Name, `{"value":"parent"}`), finalTurn()}})
	childCall := expect.Call(tool, func(p *echoPayload) error {
		if p.Value != "child" {
			return errors.New("not child")
		}
		return nil
	}, nil)
	parentCall := expect.Call(tool, func(p *echoPayload) error {
		if p.Value != "parent" {
			return errors.New("not parent")
		}
		return nil
	}, nil)
	delegate := expect.Call(tools.TypedTool[any, any]{Name: "child.run"}, nil, nil)
	assertChecksPass(t, expect.Trajectory(tr), expect.Exact(delegate, childCall, parentCall), expect.MaxModelCalls(5), expect.MaxTokens(25))
	assertChecksPass(t, expect.TopLevel(expect.Trajectory(tr)), expect.Exact(delegate, parentCall), expect.MaxModelCalls(3), expect.MaxTokens(15))
	checks := expect.Evaluate(expect.TopLevel(expect.Trajectory(tr)), childCall)
	assert.False(t, checks[0].Passed)
	var child *trajectory.ToolStep
	// Locate the parent-owned child by its real tool step, regardless of observer steps.
	for _, step := range tr.Steps {
		if step.Tool != nil && len(step.Tool.Children) != 0 {
			child = step.Tool
		}
	}
	require.NotNil(t, child)
	require.Len(t, child.Children, 1)
	assertChecksPass(t, expect.Session(trajectory.Session{child.Children[0], tr}), expect.Exact(childCall, delegate, childCall, parentCall), expect.Completed(), expect.MaxModelCalls(7))
	checks = expect.Evaluate(expect.Trajectory(tr), expect.Forbid(tool))
	assert.False(t, checks[0].Passed)
	assertChecksPass(t, expect.TopLevel(expect.Session(trajectory.Session{child.Children[0]})), expect.Exact(childCall), expect.AllAttemptsSuccessful(tool))
}

func TestTrajectoryAbandonedBudgetAndCompletion(t *testing.T) {
	tr := recordedTrajectory(t, runtimeScenario{abort: true, turns: []agenttest.Turn{finalTurn()}})
	assert.Equal(t, 1, tr.Totals.ModelCalls)
	assert.Zero(t, tr.Totals.AdoptedModelCalls)
	assertChecksPass(t, expect.Trajectory(tr), expect.MaxModelCalls(0), expect.MaxTokens(5))
	checks := expect.Evaluate(expect.Trajectory(tr), expect.Completed(), expect.MaxTokens(4))
	assert.False(t, checks[0].Passed)
	assert.False(t, checks[1].Passed)
	good := recordedTrajectory(t, runtimeScenario{turns: []agenttest.Turn{finalTurn()}})
	checks = expect.Evaluate(expect.Session(trajectory.Session{good, tr}), expect.Completed())
	assert.False(t, checks[0].Passed)
}

func TestTrajectoryAbsentResultAndBounds(t *testing.T) {
	tool := runtimeEchoTool()
	tr := recordedTrajectory(t, runtimeScenario{turns: []agenttest.Turn{toolTurn(tool.Name, `{"value":"fail"}`), finalTurn()}})
	checks := expect.Evaluate(expect.Trajectory(tr), expect.Call(tool, nil, func(*echoResult) error {
		t.Error("failed call decoded as success")
		return nil
	}), expect.Bounds(tool, nil))
	require.Len(t, checks, 2)
	assert.False(t, checks[0].Passed)
	assert.False(t, checks[1].Passed)
}

func TestTrajectoryAwaitKinds(t *testing.T) {
	awaiting := planner.NewAwait(
		planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "clarify", Question: "which?", MissingFields: []string{"value"}}),
		planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "questions", ToolName: "echo.echo", ToolCallID: "qcall", Payload: rawjson.Message(`{"value":"q"}`), Questions: []planner.AwaitQuestion{{ID: "q", Prompt: "pick", Options: []planner.AwaitQuestionOption{{ID: "yes", Label: "Yes"}}}}}),
		planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "external", Items: []planner.AwaitToolItem{{Name: "echo.echo", ToolCallID: "ecall", Payload: rawjson.Message(`{"value":"e"}`)}}}),
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "typed", Title: "enter", Schema: rawjson.Message(`{"type":"object"}`)}),
	)
	for index, tc := range []struct {
		kind      planner.AwaitItemKind
		predicate func(planner.AwaitItem) error
	}{
		{planner.AwaitItemKindClarification, func(item planner.AwaitItem) error {
			require.NotNil(t, item.Clarification)
			assert.Equal(t, "which?", item.Clarification.Question)
			assert.Equal(t, []string{"value"}, item.Clarification.MissingFields)
			return nil
		}},
		{planner.AwaitItemKindQuestions, func(item planner.AwaitItem) error {
			require.NotNil(t, item.Questions)
			assert.Equal(t, "pick", item.Questions.Questions[0].Prompt)
			assert.Equal(t, "Yes", item.Questions.Questions[0].Options[0].Label)
			assert.Equal(t, "qcall", item.Questions.ToolCallID)
			return nil
		}},
		{planner.AwaitItemKindExternalTools, func(item planner.AwaitItem) error {
			require.NotNil(t, item.ExternalTools)
			assert.Equal(t, tools.Ident("echo.echo"), item.ExternalTools.Items[0].Name)
			assert.Equal(t, "ecall", item.ExternalTools.Items[0].ToolCallID)
			return nil
		}},
		{planner.AwaitItemKindTypedInput, func(item planner.AwaitItem) error {
			require.NotNil(t, item.TypedInput)
			assert.Equal(t, "enter", item.TypedInput.Title)
			assert.JSONEq(t, `{"type":"object"}`, string(item.TypedInput.Schema))
			return nil
		}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			tr := recordedTrajectory(t, runtimeScenario{await: planner.NewAwait(awaiting.Items[index]), turns: []agenttest.Turn{finalTurn(), finalTurn()}})
			assertChecksPass(t, expect.Trajectory(tr), expect.Await(tc.kind, tc.predicate), expect.Await(tc.kind, nil))
			checks := expect.Evaluate(expect.Trajectory(tr), expect.Await(tc.kind, func(planner.AwaitItem) error {
				return errors.New("reject await")
			}))
			assert.False(t, checks[0].Passed)
			assert.Contains(t, checks[0].Diagnostic, "reject await")
		})
	}
}

func assertChecksPass(t *testing.T, subject expect.Subject, expectations ...expect.Expectation) {
	t.Helper()
	for _, check := range expect.Evaluate(subject, expectations...) {
		assert.True(t, check.Passed, check.Name+": "+check.Diagnostic)
		assert.Empty(t, check.Diagnostic)
	}
}
