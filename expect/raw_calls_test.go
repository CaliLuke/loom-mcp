package expect_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

func TestRawCallPreservesJSON(t *testing.T) {
	for _, data := range []string{`{"id":9007199254740993,"nested":[1.00000000000000001]}`, " { \"nested\" : [1.00000000000000001], \"id\" : 9007199254740993 } ", `null`} {
		t.Run(data, func(t *testing.T) {
			calls := expect.Calls([]model.ToolCall{{Name: "dynamic.lookup", Payload: rawjson.Message(data)}})
			checks := expect.Evaluate(calls, expect.RawCall(tools.Ident("dynamic.lookup"), func(got rawjson.Message) error {
				assert.Equal(t, data, string(got))
				return nil
			}, nil))
			assert.True(t, checks[0].Passed, checks[0].Diagnostic)
		})
	}
}

func TestRawCallExecutionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     rawjson.Message
		failure    *planner.ToolError
		expect     func(tools.Named) expect.Expectation
		passed     bool
		diagnostic string
	}{
		{"successful result", rawjson.Message(`{"n":9007199254740993}`), nil, func(tool tools.Named) expect.Expectation {
			return expect.RawCall(tool, nil, func(got rawjson.Message) error {
				assert.Equal(t, `{"n":9007199254740993}`, string(got))
				return nil
			})
		}, true, ""},
		{"null result", rawjson.Message(`null`), nil, func(tool tools.Named) expect.Expectation {
			return expect.RawCall(tool, nil, func(got rawjson.Message) error {
				assert.Equal(t, "null", string(got))
				return nil
			})
		}, true, ""},
		{"failed result", rawjson.Message(`{}`), planner.NewToolError("boom"), rawSuccessExpectation, false, "tool failed"},
		{"missing result", nil, nil, rawSuccessExpectation, false, "missing result"},
		{"empty result", rawjson.Message{}, nil, rawSuccessExpectation, false, "missing result"},
		{"malformed result", rawjson.Message(`{`), nil, rawSuccessExpectation, false, "malformed JSON result"},
		{"failed", nil, planner.NewToolError("boom"), func(tool tools.Named) expect.Expectation {
			return expect.RawFailed(tool, nil, func(failure *planner.ToolError) error {
				assert.Equal(t, "boom", failure.Message)
				return nil
			})
		}, true, ""},
		{"failure nil predicate", nil, planner.NewToolError("boom"), func(tool tools.Named) expect.Expectation {
			return expect.RawFailed(tool, nil, nil)
		}, true, ""},
		{"success cannot fail", rawjson.Message(`{}`), nil, func(tool tools.Named) expect.Expectation {
			return expect.RawFailed(tool, nil, func(*planner.ToolError) error {
				t.Error("successful call invoked failure predicate")
				return nil
			})
		}, false, "did not fail"},
		{"result predicate error", rawjson.Message(`{}`), nil, func(tool tools.Named) expect.Expectation {
			return expect.RawCall(tool, nil, func(rawjson.Message) error {
				return errors.New("result rejected")
			})
		}, false, "result rejected"},
		{"failure predicate error", nil, planner.NewToolError("boom"), func(tool tools.Named) expect.Expectation {
			return expect.RawFailed(tool, nil, func(*planner.ToolError) error {
				return errors.New("failure rejected")
			})
		}, false, "failure rejected"},
		{"argument-only failure", nil, planner.NewToolError("boom"), func(tool tools.Named) expect.Expectation {
			return expect.RawCall(tool, nil, nil)
		}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(`{}`), Result: tc.result, Error: tc.failure}
			checks := expect.Evaluate(rawToolSubject(tool, true), tc.expect(tool.Name))
			assert.Equal(t, tc.passed, checks[0].Passed, checks[0].Diagnostic)
			assert.Contains(t, checks[0].Diagnostic, tc.diagnostic)
			if !tc.passed {
				assert.Contains(t, checks[0].Diagnostic, "dynamic.lookup")
			}
		})
	}
}

func TestRawCallInvalidArguments(t *testing.T) {
	for _, data := range []string{"", " ", "{", `{"n":1,"n":2}`, "{} {}", string([]byte{'"', 0xff, '"'})} {
		t.Run(data, func(t *testing.T) {
			predicate := func(rawjson.Message) error {
				t.Error("invalid arguments invoked predicate")
				return nil
			}
			call := &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(data), Error: planner.NewToolError("boom")}
			for _, expectation := range []expect.Expectation{expect.RawCall(call.Name, nil, nil), expect.RawCall(call.Name, predicate, nil), expect.RawFailed(call.Name, predicate, nil)} {
				checks := expect.Evaluate(rawToolSubject(call, true), expectation)
				assert.False(t, checks[0].Passed)
				assert.Contains(t, checks[0].Diagnostic, "dynamic.lookup")
				assert.Contains(t, checks[0].Diagnostic, "malformed JSON arguments")
			}
		})
	}
}

func TestRawExecutionRequiresVerifiedEvidence(t *testing.T) {
	for _, failure := range []*planner.ToolError{nil, planner.NewToolError("boom")} {
		predicate := func(rawjson.Message) error {
			t.Error("unverified execution invoked predicate")
			return nil
		}
		tool := &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(`{}`), Result: rawjson.Message(`{}`), Error: failure}
		for _, expectation := range []expect.Expectation{expect.RawCall(tool.Name, predicate, predicate), expect.RawFailed(tool.Name, predicate, func(*planner.ToolError) error {
			t.Error("unverified execution invoked failure predicate")
			return nil
		})} {
			checks := expect.Evaluate(rawToolSubject(tool, false), expectation)
			assert.False(t, checks[0].Passed)
			assert.Contains(t, checks[0].Diagnostic, "dynamic.lookup")
			assert.Contains(t, checks[0].Diagnostic, "evidence unverified")
		}
	}
}

func TestRawExecutionNotApplicableToCalls(t *testing.T) {
	tool := tools.Ident("dynamic.lookup")
	subject := expect.Calls([]model.ToolCall{{Name: tool, Payload: rawjson.Message(`{}`)}})
	for _, expectation := range []expect.Expectation{rawSuccessExpectation(tool), expect.RawFailed(tool, nil, nil)} {
		checks := expect.Evaluate(subject, expectation)
		assert.False(t, checks[0].Passed)
		assert.Equal(t, "not applicable to a model-call subject", checks[0].Diagnostic)
	}
}

func TestRawCallsUseRecordVerification(t *testing.T) {
	record := recordedRun(t, runtimeScenario{turns: []agenttest.Turn{toolTurn("echo.echo", `{"value":"fail"}`), toolTurn("echo.echo", `{"value":"good"}`), finalTurn()}})
	tr, err := trajectory.Build(record, "run", trajectory.Options{EventsUnverified: true})
	require.NoError(t, err)
	assertChecksPass(t, expect.Trajectory(tr), expect.RawFailed(tools.Ident("echo.echo"), nil, nil), expect.RawCall(tools.Ident("echo.echo"), nil, rawResultValue(`"good!"`)))
	assert.False(t, tr.StatusVerified)
}

func TestRawFailedSkipsSuccessfulExecutions(t *testing.T) {
	tr := &trajectory.Trajectory{Steps: []trajectory.Step{
		{Kind: trajectory.StepTool, Verified: true, Tool: &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(`{}`), Result: rawjson.Message(`{}`)}},
		{Kind: trajectory.StepTool, Verified: true, Tool: &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(`{}`), Error: planner.NewToolError("boom")}},
	}}
	assertChecksPass(t, expect.Trajectory(tr), expect.RawFailed(tools.Ident("dynamic.lookup"), nil, nil))
	checks := expect.Evaluate(expect.Trajectory(tr), expect.RawFailed(tools.Ident("dynamic.lookup"), nil, func(*planner.ToolError) error {
		return errors.New("rejected failure")
	}))
	assert.False(t, checks[0].Passed)
	assert.Contains(t, checks[0].Diagnostic, "rejected failure")
	checks = expect.Evaluate(expect.Trajectory(tr), expect.Exact(expect.RawFailed(tools.Ident("dynamic.lookup"), nil, nil), expect.RawCall(tools.Ident("dynamic.lookup"), nil, nil)))
	assert.False(t, checks[0].Passed)
	assert.Contains(t, checks[0].Diagnostic, "did not fail")
}

func TestRawCallOrdering(t *testing.T) {
	tool := tools.Ident("dynamic.lookup")
	a := model.ToolCall{Name: tool, Payload: rawjson.Message(`"a"`)}
	b := model.ToolCall{Name: tool, Payload: rawjson.Message(`"b"`)}
	for _, tc := range []struct {
		name   string
		calls  []model.ToolCall
		exact  bool
		passed []bool
	}{
		{"ordered", []model.ToolCall{a, b}, false, []bool{true, true}},
		{"reversed", []model.ToolCall{b, a}, false, []bool{true, false}},
		{"exact", []model.ToolCall{a, b}, true, []bool{true}},
		{"exact extra", []model.ToolCall{a, b, a}, true, []bool{false}},
		{"exact positional", []model.ToolCall{b, a}, true, []bool{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectations := []expect.Expectation{expect.RawCall(tool, rawEquals(`"a"`), nil), expect.RawCall(tool, rawEquals(`"b"`), nil)}
			if tc.exact {
				expectations = []expect.Expectation{expect.Exact(expectations...)}
			}
			checks := expect.Evaluate(expect.Calls(tc.calls), expectations...)
			require.Len(t, checks, len(tc.passed))
			for i, passed := range tc.passed {
				assert.Equal(t, passed, checks[i].Passed, checks[i].Diagnostic)
			}
		})
	}
}

func TestRawPredicatesOwnJSON(t *testing.T) {
	tool := &trajectory.ToolStep{Name: "dynamic.lookup", Arguments: rawjson.Message(`{}`), Result: rawjson.Message(`{}`)}
	mutate := func(data rawjson.Message) error {
		data[0] = '['
		return nil
	}
	assertChecksPass(t, rawToolSubject(tool, true), expect.RawCall(tool.Name, mutate, mutate))
	assert.Equal(t, "{}", string(tool.Arguments))
	assert.Equal(t, "{}", string(tool.Result))
}

func TestRawCallsRuntimeAndChildren(t *testing.T) {
	tr := recordedTrajectory(t, runtimeScenario{child: true, turns: []agenttest.Turn{toolTurn("child.run", `{}`), toolTurn("echo.echo", `{"value":"fail"}`), toolTurn("echo.echo", `{"value":"parent"}`), finalTurn()}})
	delegate := expect.RawCall(tools.Ident("child.run"), nil, nil)
	child := expect.RawCall(tools.Ident("echo.echo"), rawEquals(`{"value":"child"}`), rawResultValue(`"child!"`))
	failed := expect.RawFailed(tools.Ident("echo.echo"), rawEquals(`{"value":"fail"}`), nil)
	parent := expect.RawCall(tools.Ident("echo.echo"), rawEquals(`{"value":"parent"}`), rawResultValue(`"parent!"`))
	assertChecksPass(t, expect.Trajectory(tr), expect.Exact(delegate, child, failed, parent))
	assertChecksPass(t, expect.TopLevel(expect.Trajectory(tr)), expect.Exact(delegate, failed, parent))
	checks := expect.Evaluate(expect.TopLevel(expect.Trajectory(tr)), child)
	assert.False(t, checks[0].Passed)
	for _, step := range tr.Steps {
		if step.Tool != nil && len(step.Tool.Children) > 0 {
			assertChecksPass(t, expect.TopLevel(expect.Session(trajectory.Session{step.Tool.Children[0]})), expect.Exact(child))
			return
		}
	}
	t.Fatal("missing child trajectory")
}

func rawSuccessExpectation(tool tools.Named) expect.Expectation {
	return expect.RawCall(tool, nil, func(rawjson.Message) error {
		return nil
	})
}

func rawToolSubject(tool *trajectory.ToolStep, verified bool) expect.Subject {
	return expect.Trajectory(&trajectory.Trajectory{Steps: []trajectory.Step{{Kind: trajectory.StepTool, Verified: verified, Tool: tool}}})
}

func rawEquals(want string) func(rawjson.Message) error {
	return func(got rawjson.Message) error {
		if string(got) != want {
			return fmt.Errorf("raw JSON = %s, want %s", got, want)
		}
		return nil
	}
}

func rawResultValue(want string) func(rawjson.Message) error {
	return func(got rawjson.Message) error {
		var object map[string]jsontext.Value
		if err := json.Unmarshal(got, &object); err != nil {
			return err
		}
		return rawEquals(want)(rawjson.Message(object["value"]))
	}
}
