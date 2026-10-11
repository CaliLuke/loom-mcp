package eval_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimSubjectsAndPlaceholders(t *testing.T) {
	tests := []struct {
		name   string
		claims []eval.Claim
		task   string
		items  eval.Dataset
		want   string
	}{
		{name: "unknown", claims: []eval.Claim{{ID: "a", Text: "ok", Subject: "unknown"}}, want: "subject"},
		{name: "tool on answer", claims: []eval.Claim{{ID: "a", Text: "ok", Tool: "search"}}, want: "step"},
		{name: "missing task", claims: []eval.Claim{eval.TaskCompletion()}, want: "{task}"},
		{name: "partial item tasks", claims: []eval.Claim{eval.TaskCompletion()}, items: eval.Items(eval.Item{ID: "a", Input: "hi", Task: "task"}, eval.Item{ID: "b", Input: "hi"}), want: "{task}"},
		{name: "missing outcome", claims: []eval.Claim{eval.ConversationCompleteness()}, want: "{expected_outcome}"},
		{name: "collision", claims: []eval.Claim{eval.ToolArgumentCorrectness(tools.Ident("a.b")), eval.ToolArgumentCorrectness(tools.Ident("a-b"))}, want: "duplicate"},
		{name: "scenario task", claims: []eval.Claim{eval.TaskCompletion()}, task: "task"},
		{name: "item task", claims: []eval.Claim{eval.TaskCompletion()}, items: eval.Items(eval.Item{ID: "a", Input: "hi", Task: "task"})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := validScenario()
			s.Claims = tc.claims
			s.Task = tc.task
			if tc.items.Len() > 0 {
				s.Items = tc.items
			}
			err := eval.Validate(s)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBuiltInClaims(t *testing.T) {
	assert.Equal(t, eval.Claim{ID: "task_completion", Subject: eval.SubjectTrajectory, Text: "The agent completed the task: {task}."}, eval.TaskCompletion())
	assert.Equal(t, eval.Claim{ID: "step_efficiency", Subject: eval.SubjectTrajectory, Text: "The agent took no unnecessary steps for the task."}, eval.StepEfficiency())
	assert.Equal(t, eval.Claim{ID: "conversation_completeness", Subject: eval.SubjectSession, Text: "The conversation achieved the expected outcome: {expected_outcome}."}, eval.ConversationCompleteness())
	assert.Equal(t, eval.Claim{ID: "argument_correctness", Subject: eval.SubjectStep, Text: "The arguments of this call are correct given the conversation so far."}, eval.ArgumentCorrectness())
	c := eval.ToolArgumentCorrectness(tools.Ident("Search.Query"))
	assert.Equal(t, "argument_correctness_search_query", c.ID)
	assert.Equal(t, tools.Ident("Search.Query"), c.Tool)
	assert.Len(t, eval.ToolArgumentCorrectness(tools.Ident(strings.Repeat("X", 100))).ID, 64)
}

func TestExecuteResolvesClaims(t *testing.T) {
	s := customScenario(syntheticSession())
	s.Task = "default"
	s.Claims = []eval.Claim{eval.TaskCompletion(), eval.ConversationCompleteness(), {ID: "answer", Text: "answer"}}
	s.Items = eval.Items(eval.Item{ID: "one", Turns: []string{"hi"}, Task: "item task", ExpectedOutcome: "done"})
	out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.NoError(t, out.Err)
	assert.Equal(t, "The agent completed the task: item task.", out.Claims[0].Text)
	assert.Equal(t, "The conversation achieved the expected outcome: done.", out.Claims[1].Text)
	assert.Equal(t, eval.SubjectAnswer, out.Claims[2].Subject)
	assert.Contains(t, s.Claims[0].Text, "{task}")
	s = customScenario(syntheticSession())
	s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Claims: []eval.Claim{eval.TaskCompletion()}}, nil
	}
	out, err = eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.ErrorContains(t, out.Err, "{task}")
}

func TestJudgedVerdicts(t *testing.T) {
	tests := []struct {
		name      string
		claims    []eval.Claim
		judgments []eval.Judgment
		checks    []expect.Check
		want      eval.Verdict
	}{
		{name: "entailed", judgments: []eval.Judgment{{ClaimID: "a", Label: eval.Entailed}}, want: eval.VerdictPassed},
		{name: "contradicted", judgments: []eval.Judgment{{ClaimID: "a", Label: eval.Contradicted}}, want: eval.VerdictFailed},
		{name: "informational", claims: []eval.Claim{{ID: "a", Informational: true}}, judgments: []eval.Judgment{{ClaimID: "a", Label: eval.Contradicted, Informational: true}}, want: eval.VerdictPassed},
		{name: "pending", want: eval.VerdictPending},
		{name: "judge error", judgments: []eval.Judgment{{ClaimID: "a", Err: errors.New("provider")}}, checks: []expect.Check{{Name: "failed"}}, want: eval.VerdictError},
		{name: "failed before pending", checks: []expect.Check{{Name: "failed"}}, want: eval.VerdictFailed},
		{name: "step unit fails", judgments: []eval.Judgment{{ClaimID: "a", Label: eval.Entailed}, {ClaimID: "a", Label: eval.NotAddressed}}, want: eval.VerdictFailed},
		{name: "pending before failed claim", claims: []eval.Claim{{ID: "pending"}, {ID: "a"}}, judgments: []eval.Judgment{{ClaimID: "a", Label: eval.Indeterminate}}, want: eval.VerdictFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := tc.claims
			if claims == nil {
				claims = []eval.Claim{{ID: "a"}}
			}
			out := eval.Outcome{Claims: claims, Judgments: tc.judgments, Checks: tc.checks}
			assert.Equal(t, tc.want, out.Verdict())
		})
	}
}

func TestRecursiveMetrics(t *testing.T) {
	session := syntheticSession()
	session.Trajectory[0].Totals = trajectory.Totals{ModelCalls: 99, AdoptedModelCalls: 2, ToolCalls: 3, FailedToolCalls: 1}
	session.Trajectory[0].Steps = []trajectory.Step{{Kind: trajectory.StepTool, Tool: &trajectory.ToolStep{Children: []*trajectory.Trajectory{{Totals: trajectory.Totals{AdoptedModelCalls: 4, ToolCalls: 5, RepeatedToolCalls: 2}}}}}}
	session.Trajectory = append(session.Trajectory, &trajectory.Trajectory{Totals: trajectory.Totals{AdoptedModelCalls: 1, ToolCalls: 2, FailedToolCalls: 2, RepeatedToolCalls: 1}})
	s := customScenario(session)
	s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Scores: []expect.Score{{Name: "tool_calls", Value: 42}}}, nil
	}
	out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.NoError(t, out.Err)
	assert.Equal(t, []expect.Score{{Name: "model_calls", Value: 7}, {Name: "tool_calls", Value: 10}, {Name: "failed_tool_calls", Value: 3}, {Name: "repeated_tool_calls", Value: 3}, {Name: "tool_calls#2", Value: 42}}, out.Scores)
}

func TestExecuteResolvesDeclaredClaimsBeforeDrive(t *testing.T) {
	s := customScenario(syntheticSession())
	s.Task = "literal {expected_outcome}"
	s.Claims = []eval.Claim{eval.TaskCompletion()}
	s.Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
		return nil, errors.New("drive failed")
	}
	out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.ErrorContains(t, out.Err, "drive failed")
	assert.Equal(t, "The agent completed the task: literal {expected_outcome}.", out.Claims[0].Text)
	assert.Equal(t, eval.SubjectTrajectory, out.Claims[0].Subject)
	// Inserted values are literal, even when Assert adds more claims.
	s.Drive = customScenario(syntheticSession()).Drive
	s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Claims: []eval.Claim{{ID: "extra", Text: "{task}"}}}, nil
	}
	out, err = eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.NoError(t, out.Err)
	assert.Equal(t, "literal {expected_outcome}", out.Claims[1].Text)
}
