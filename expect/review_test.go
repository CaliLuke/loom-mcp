package expect_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservedEvidenceUnverified(t *testing.T) {
	awaits := []planner.AwaitItem{
		planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "clarify", Question: "which?"}),
		planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "questions", ToolName: "echo.echo", ToolCallID: "qcall", Payload: rawjson.Message(`{"value":"q"}`)}),
		planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "external", Items: []planner.AwaitToolItem{{Name: "echo.echo", ToolCallID: "ecall", Payload: rawjson.Message(`{"value":"e"}`)}}}),
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "typed", Schema: rawjson.Message(`{"type":"object"}`)}),
	}
	for _, item := range awaits {
		t.Run(string(item.Kind), func(t *testing.T) {
			record := recordedRun(t, runtimeScenario{await: planner.NewAwait(item), turns: []agenttest.Turn{finalTurn(), finalTurn()}})
			tr, err := trajectory.Build(record, "run", trajectory.Options{EventsUnverified: true})
			require.NoError(t, err)
			assert.Equal(t, "success", tr.Status)
			checks := expect.Evaluate(expect.Trajectory(tr), expect.Completed(), expect.Await(item.Kind, func(planner.AwaitItem) error {
				t.Error("unverified await invoked predicate")
				return nil
			}))
			require.Len(t, checks, 2)
			assert.False(t, checks[0].Passed)
			assert.Equal(t, "evidence unverified: status", checks[0].Diagnostic)
			assert.False(t, checks[1].Passed)
			assert.Equal(t, "evidence unverified: await_"+string(item.Kind), checks[1].Diagnostic)
			assertChecksPass(t, expect.Trajectory(tr), expect.MaxModelCalls(2), expect.MaxTokens(10))
		})
	}
}

func TestAllAttemptsSuccessfulNeedsCall(t *testing.T) {
	for _, turns := range [][]agenttest.Turn{
		{finalTurn()},
		{toolTurn("echo.echo", `{"value":"good"}`), finalTurn()},
	} {
		tr := recordedTrajectory(t, runtimeScenario{turns: turns})
		checks := expect.Evaluate(expect.Trajectory(tr), expect.AllAttemptsSuccessful(tools.Ident("never.called")))
		assert.False(t, checks[0].Passed)
		assert.Equal(t, "no call to never.called", checks[0].Diagnostic)
	}
}

func TestExpectedToolsMalformedCallArguments(t *testing.T) {
	for _, malformed := range []string{"", "{", `{"value":}`} {
		for _, args := range []json.RawMessage{nil, json.RawMessage(`{"value":"good"}`)} {
			t.Run(malformed+"/"+string(args), func(t *testing.T) {
				calls := expect.Calls([]model.ToolCall{
					{Name: "echo.echo", Payload: rawjson.Message(malformed)},
					{Name: "echo.echo", Payload: rawjson.Message(`{"value":"good"}`)},
				})
				expected := expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "echo.echo", Args: args}}, Exact: true}
				check, score := expected.Evaluate(calls)
				assert.False(t, check.Passed)
				assert.InDelta(t, .5, score.Value, 1e-12)
				assert.Contains(t, check.Diagnostic, "call 1 arguments for echo.echo")
				check, score = expected.Evaluate(expect.Calls([]model.ToolCall{{Name: "echo.echo", Payload: rawjson.Message(malformed)}}))
				assert.False(t, check.Passed)
				assert.Zero(t, score.Value)
				assert.Contains(t, check.Diagnostic, "call 1 arguments for echo.echo")
			})
		}
	}
}

func TestAwaitDecodesOnlyRequestedKind(t *testing.T) {
	for _, requestedMalformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "other kind malformed", true: "requested kind malformed"}[requestedMalformed], func(t *testing.T) {
			record := recordedRun(t, runtimeScenario{await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "clarify", Question: "which?"})), turns: []agenttest.Turn{finalTurn(), finalTurn()}})
			for i := range record.Runs {
				run := &record.Runs[i]
				for j, event := range run.Events {
					if event.Type != hooks.AwaitClarification {
						continue
					}
					bad := *event
					if requestedMalformed {
						bad.Payload = rawjson.Message(`{"Question":123}`)
						run.Events[j] = &bad
					} else {
						bad.Type = hooks.AwaitQuestions
						bad.Payload = rawjson.Message(`{"Questions":"bad"}`)
						run.Events = append(run.Events[:j:j], append([]*runlog.Event{&bad}, run.Events[j:]...)...)
					}
					break
				}
			}
			tr, err := trajectory.Build(record, "run", trajectory.Options{})
			require.NoError(t, err)
			checks := expect.Evaluate(expect.Trajectory(tr), expect.Await(planner.AwaitItemKindClarification, nil))
			assert.Equal(t, !requestedMalformed, checks[0].Passed, checks[0].Diagnostic)
			if requestedMalformed {
				assert.Contains(t, checks[0].Diagnostic, "decode await:")
				assert.Contains(t, checks[0].Diagnostic, `"`)
			}
		})
	}
}

func TestFailedSuccessfulCallsDiagnostic(t *testing.T) {
	tool := runtimeEchoTool()
	for _, tc := range []struct {
		name       string
		turns      []agenttest.Turn
		args       func(*echoPayload) error
		diagnostic string
	}{
		{"success only", []agenttest.Turn{toolTurn(tool.Name, `{"value":"good"}`), finalTurn()}, nil, "call to echo.echo did not fail"},
		{"success matching arguments", []agenttest.Turn{toolTurn(tool.Name, `{"value":"good"}`), finalTurn()}, func(p *echoPayload) error {
			if p.Value != "good" {
				return errors.New("wrong argument")
			}
			return nil
		}, "call to echo.echo did not fail"},
		{"success wrong arguments", []agenttest.Turn{toolTurn(tool.Name, `{"value":"good"}`), finalTurn()}, func(*echoPayload) error {
			return errors.New("wrong argument")
		}, "wrong argument"},
		{"no call", []agenttest.Turn{finalTurn()}, nil, "no matching call to echo.echo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := recordedTrajectory(t, runtimeScenario{turns: tc.turns})
			checks := expect.Evaluate(expect.Trajectory(tr), expect.Failed(tool, tc.args, nil))
			assert.False(t, checks[0].Passed)
			assert.Contains(t, checks[0].Diagnostic, tc.diagnostic)
			checks = expect.Evaluate(expect.Trajectory(tr), expect.Exact(expect.Failed(tool, tc.args, nil)))
			assert.False(t, checks[0].Passed)
			assert.Contains(t, checks[0].Diagnostic, tc.diagnostic)
		})
	}
}
