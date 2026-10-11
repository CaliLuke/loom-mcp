package expect_test

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type echoPayload struct {
	Value string `json:"value"`
}
type echoResult struct {
	Value string `json:"value"`
}

func TestCallMatching(t *testing.T) {
	tool := echoTool()
	a := modelCall(tool.Name, "a")
	b := modelCall(tool.Name, "b")
	other := modelCall("other.tool", "a")
	anyCall := func() expect.Expectation {
		return expect.Call(tool, nil, nil)
	}
	valueCall := func(value string) expect.Expectation {
		return expect.Call(tool, func(p *echoPayload) error {
			if p.Value != value {
				return errors.New("wanted " + value)
			}
			return nil
		}, nil)
	}
	for _, tc := range []struct {
		name         string
		calls        []model.ToolCall
		expectations []expect.Expectation
		passed       []bool
	}{
		{"in order", []model.ToolCall{a, b}, []expect.Expectation{valueCall("a"), valueCall("b")}, []bool{true, true}},
		{"reversed", []model.ToolCall{b, a}, []expect.Expectation{valueCall("a"), valueCall("b")}, []bool{true, false}},
		{"missing", nil, []expect.Expectation{anyCall()}, []bool{false}},
		{"extras allowed", []model.ToolCall{other, a, other}, []expect.Expectation{anyCall()}, []bool{true}},
		{"call cannot bind twice", []model.ToolCall{a}, []expect.Expectation{anyCall(), anyCall()}, []bool{true, false}},
		{"greedy no backtracking", []model.ToolCall{a, b}, []expect.Expectation{anyCall(), valueCall("a")}, []bool{true, false}},
		{"skip unsatisfying", []model.ToolCall{b, a}, []expect.Expectation{valueCall("a")}, []bool{true}},
		{"exact pass", []model.ToolCall{a, b}, []expect.Expectation{expect.Exact(valueCall("a"), valueCall("b"))}, []bool{true}},
		{"exact extra", []model.ToolCall{other, a}, []expect.Expectation{expect.Exact(valueCall("a"))}, []bool{false}},
		{"exact trailing extra", []model.ToolCall{a, other}, []expect.Expectation{expect.Exact(valueCall("a"))}, []bool{false}},
		{"exact too few", []model.ToolCall{a}, []expect.Expectation{expect.Exact(anyCall(), anyCall())}, []bool{false}},
		{"exact reversed", []model.ToolCall{b, a}, []expect.Expectation{expect.Exact(valueCall("a"), valueCall("b"))}, []bool{false}},
		{"exact empty", nil, []expect.Expectation{expect.Exact()}, []bool{true}},
		{"exact empty with calls", []model.ToolCall{a}, []expect.Expectation{expect.Exact()}, []bool{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := expect.Evaluate(expect.Calls(tc.calls), tc.expectations...)
			require.Len(t, checks, len(tc.passed))
			for i, check := range checks {
				assert.Equal(t, tc.passed[i], check.Passed, check.Diagnostic)
				if check.Passed {
					assert.Empty(t, check.Diagnostic)
				} else {
					assert.NotEmpty(t, check.Diagnostic)
				}
			}
		})
	}
}

func TestDecoded(t *testing.T) {
	tool := echoTool()
	for _, tc := range []struct {
		name       string
		codec      tools.JSONCodec[*echoPayload]
		predicate  func(*echoPayload) error
		payload    string
		passed     bool
		diagnostic string
	}{
		{"typed argument", tool.Payload, func(p *echoPayload) error {
			assert.Equal(t, "a", p.Value)
			return nil
		}, `{"value":"a"}`, true, ""},
		{"nil predicate still decodes", tool.Payload, nil, `{"value":"a"}`, true, ""},
		{"predicate failure", tool.Payload, func(*echoPayload) error {
			return errors.New("argument rejected")
		}, `{"value":"a"}`, false, "argument rejected"},
		{"codec failure quoted", tools.JSONCodec[*echoPayload]{FromJSON: func([]byte) (*echoPayload, error) {
			return nil, errors.New("codec says no")
		}}, nil, `{}`, false, `"codec says no"`},
		{"malformed JSON", tool.Payload, nil, `{`, false, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := expect.Calls([]model.ToolCall{{Name: tool.Name, Payload: rawjson.Message(tc.payload)}})
			descriptor := tool
			descriptor.Payload = tc.codec
			callPredicate := tc.predicate
			if callPredicate == nil {
				callPredicate = func(*echoPayload) error {
					return nil
				}
			}
			for _, expectation := range []expect.Expectation{expect.Decoded(tool, tc.codec, tc.predicate), expect.Call(descriptor, callPredicate, nil)} {
				checks := expect.Evaluate(calls, expectation)
				require.Len(t, checks, 1)
				assert.Equal(t, tc.passed, checks[0].Passed)
				assert.Contains(t, checks[0].Diagnostic, tc.diagnostic)
			}
		})
	}
}

func TestCallNilPredicates(t *testing.T) {
	tool := echoTool()
	tool.Payload.FromJSON = func([]byte) (*echoPayload, error) {
		t.Error("Call with nil predicates must not decode arguments")
		return nil, errors.New("unexpected decode")
	}
	checks := expect.Evaluate(expect.Calls([]model.ToolCall{modelCall(tool.Name, "a")}), expect.Call(tool, nil, nil))
	require.Len(t, checks, 1)
	assert.True(t, checks[0].Passed, checks[0].Diagnostic)
}

func TestForbid(t *testing.T) {
	tool := echoTool()
	for _, tc := range []struct {
		name      string
		forbidden []tools.Named
		passed    bool
	}{
		{"ident", []tools.Named{tool.Name}, false},
		{"descriptor", []tools.Named{tool}, false},
		{"different tool", []tools.Named{tools.Ident("other.tool")}, true},
		{"multiple", []tools.Named{tools.Ident("other.tool"), tool}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := expect.Evaluate(expect.Calls([]model.ToolCall{modelCall(tool.Name, "a")}), expect.Call(tool, nil, nil), expect.Forbid(tc.forbidden...))
			require.Len(t, checks, 2)
			assert.Equal(t, tc.passed, checks[1].Passed)
		})
	}
}

func TestExactRejectsNestedForbid(t *testing.T) {
	tool := echoTool()
	assert.Panics(t, func() {
		expect.Exact(expect.Forbid(tool))
	})
	assert.Panics(t, func() {
		expect.Exact(expect.Call(tool, nil, nil), expect.Exact(expect.Forbid(tool)))
	})
}

func echoTool() tools.TypedTool[*echoPayload, *echoResult] {
	return tools.TypedTool[*echoPayload, *echoResult]{Name: "echo.echo", Payload: tools.JSONCodec[*echoPayload]{FromJSON: func(data []byte) (*echoPayload, error) {
		var payload echoPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}}}
}

func modelCall(name tools.Ident, value string) model.ToolCall {
	return model.ToolCall{Name: name, Payload: rawjson.Message(`{"value":"` + value + `"}`)}
}
