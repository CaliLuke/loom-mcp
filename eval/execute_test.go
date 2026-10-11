package eval_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteCustomDrive(t *testing.T) {
	session := syntheticSession()
	h := &agenttest.Harness{}
	s := customScenario(session)
	s.Task = "default task"
	s.Reference = "default reference"
	s.Assert = func(ctx context.Context, a *eval.Attempt) (eval.Assertions, error) {
		assert.Same(t, h, a.Harness)
		assert.Same(t, session, a.Session)
		assert.Same(t, session.Trajectory[0].Final, a.Final())
		assert.Equal(t, "override", a.Item.Task)
		assert.Equal(t, "default reference", a.Item.Reference)
		assert.Equal(t, "case", a.Scenario)
		expect.Require(t, a.Subject(), expect.MaxModelCalls(0))
		return eval.Assertions{Claims: []eval.Claim{{ID: "extra", Text: "Extra criterion"}}}, nil
	}
	item := s.Items.Items()[0]
	item.Task = "override"
	out, err := eval.Execute(t.Context(), h, s, item)
	require.NoError(t, err)
	assert.NoError(t, out.Err)
	assert.Same(t, session, out.Session)
	assert.Equal(t, "override", out.Task)
	assert.Equal(t, "default reference", out.Reference)
	assert.Equal(t, []eval.Claim{{ID: "correct", Text: "Answers correctly"}, {ID: "extra", Text: "Extra criterion"}}, out.Claims)
}

func TestExecuteInfrastructure(t *testing.T) {
	sentinel := errors.New("callback failed")
	tests := []struct {
		name      string
		drive     func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error)
		assertion func(context.Context, *eval.Attempt) (eval.Assertions, error)
		want      error
		message   string
	}{
		{name: "drive error", drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
			return syntheticSession(), sentinel
		}, want: sentinel},
		{name: "assert error", assertion: func(context.Context, *eval.Attempt) (eval.Assertions, error) {
			return eval.Assertions{}, sentinel
		}, want: sentinel},
		{name: "drive panic", drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
			panic("drive boom")
		}, message: "drive boom"},
		{name: "assert panic", assertion: func(context.Context, *eval.Attempt) (eval.Assertions, error) {
			panic("assert boom")
		}, message: "assert boom"},
		{name: "timeout drive", drive: func(ctx context.Context, h *agenttest.Harness, item eval.Item) (*agenttest.SessionResult, error) {
			<-ctx.Done()
			return syntheticSession(), nil
		}, want: context.DeadlineExceeded},
		{name: "timeout assert", assertion: func(ctx context.Context, a *eval.Attempt) (eval.Assertions, error) {
			<-ctx.Done()
			return eval.Assertions{}, nil
		}, want: context.DeadlineExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := customScenario(syntheticSession())
			if tc.drive != nil {
				s.Drive = tc.drive
			}
			s.Assert = tc.assertion
			s.Timeout = time.Millisecond
			out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
			require.NoError(t, err)
			require.Error(t, out.Err)
			if tc.want != nil {
				require.ErrorIs(t, out.Err, tc.want)
			}
			if tc.message != "" {
				require.ErrorContains(t, out.Err, tc.message)
				require.ErrorContains(t, out.Err, "goroutine")
			}
			if tc.name == "drive error" {
				assert.NotNil(t, out.Session)
				assert.Empty(t, out.Checks)
				assert.Empty(t, out.Scores)
			}
		})
	}
}

func TestExecuteAssertionContracts(t *testing.T) {
	tests := []struct {
		name       string
		assertions eval.Assertions
		want       error
	}{
		{name: "failed without diagnostic", assertions: eval.Assertions{Checks: []expect.Check{{Name: "check"}}}},
		{name: "passed with diagnostic", assertions: eval.Assertions{Checks: []expect.Check{{Name: "check", Passed: true, Diagnostic: "bad"}}}},
		{name: "empty check name", assertions: eval.Assertions{Checks: []expect.Check{{Passed: true}}}},
		{name: "empty score name", assertions: eval.Assertions{Scores: []expect.Score{{Value: 1}}}},
		{name: "NaN", assertions: eval.Assertions{Scores: []expect.Score{{Name: "score", Value: math.NaN()}}}},
		{name: "Inf", assertions: eval.Assertions{Scores: []expect.Score{{Name: "score", Value: math.Inf(1)}}}},
		{name: "duplicate claims", assertions: eval.Assertions{Claims: []eval.Claim{{ID: "correct", Text: "Again"}}}},
		{name: "no assertions", want: eval.ErrNoAssertions},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := customScenario(syntheticSession())
			if tc.want != nil {
				s.Claims = nil
			}
			s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
				return tc.assertions, nil
			}
			out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
			require.NoError(t, err)
			require.Error(t, out.Err)
			if tc.want != nil {
				require.ErrorIs(t, out.Err, tc.want)
			}
		})
	}
}

func TestExecuteNamesAndAgentFailure(t *testing.T) {
	session := syntheticSession()
	session.Runs[0].RunErr = errors.New("agent failed")
	s := customScenario(session)
	s.Expect = []expect.Expectation{expect.Func("runs_succeeded", func(expect.Subject) error {
		return nil
	})}
	item := s.Items.Items()[0]
	item.ExpectedTools = &expect.ExpectedTools{}
	s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Checks: []expect.Check{{Name: "runs_succeeded", Passed: true}, {Name: "runs_succeeded#2", Passed: true}, {Name: "expected_tools", Passed: true}}, Scores: []expect.Score{{Name: "tool_correctness", Value: 1}, {Name: "tool_correctness", Value: 0}}}, nil
	}
	out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, item)
	require.NoError(t, err)
	assert.NoError(t, out.Err)
	assert.False(t, out.Checks[0].Passed)
	assert.Contains(t, out.Checks[0].Diagnostic, "last")
	assert.Contains(t, out.Checks[0].Diagnostic, "agent failed")
	names := make([]string, len(out.Checks))
	for n, check := range out.Checks {
		names[n] = check.Name
	}
	assert.Equal(t, []string{"runs_succeeded", "runs_succeeded#2", "expected_tools", "runs_succeeded#3", "runs_succeeded#2#2", "expected_tools#2"}, names)
	assert.Equal(t, []expect.Score{{Name: "tool_correctness", Value: 1}, {Name: "tool_correctness#2", Value: 1}, {Name: "tool_correctness#3", Value: 0}}, out.Scores)
}

func TestExecuteInvalidArguments(t *testing.T) {
	s := customScenario(syntheticSession())
	_, err := eval.Execute(t.Context(), nil, s, s.Items.Items()[0])
	require.Error(t, err)
	_, err = eval.Execute(t.Context(), &agenttest.Harness{}, s, eval.Item{ID: "invalid"})
	require.ErrorContains(t, err, `item "invalid"`)
	s.ID = "bad/id"
	_, err = eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.Error(t, err)
}

func syntheticSession() *agenttest.SessionResult {
	tr := &trajectory.Trajectory{RunID: "last", SessionID: "session", Final: &model.Message{Parts: []model.Part{model.TextPart{Text: "answer"}}}}
	return &agenttest.SessionResult{SessionID: "session", Runs: []*agenttest.Result{{RunID: "last", Trajectory: tr}}, Trajectory: trajectory.Session{tr}}
}

func customScenario(session *agenttest.SessionResult) eval.Scenario {
	s := validScenario()
	s.Agent = ""
	s.Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
		return session, nil
	}
	return s
}
