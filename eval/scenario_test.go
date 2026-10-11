package eval_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScenarioValidation(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name   string
		change func(*eval.Scenario)
	}{
		{"ID", func(s *eval.Scenario) {
			s.ID = "bad/id"
		}}, {"agent", func(s *eval.Scenario) {
			s.Agent = ""
		}},
		{"negative timeout", func(s *eval.Scenario) {
			s.Timeout = -time.Second
		}},
		{"dataset", func(s *eval.Scenario) {
			s.Items = eval.Items()
		}},
		{"file error", func(s *eval.Scenario) {
			s.Items = eval.JSONL(fstest.MapFS{}, "missing")
		}},
		{"tag", func(s *eval.Scenario) {
			s.Tags = []string{"bad/tag"}
		}},
		{"duplicate tags", func(s *eval.Scenario) {
			s.Tags = []string{"tag", "tag"}
		}},
		{"claim ID", func(s *eval.Scenario) {
			s.Claims[0].ID = "Upper"
		}},
		{"long claim ID", func(s *eval.Scenario) {
			s.Claims[0].ID = strings.Repeat("a", 65)
		}},
		{"claim text", func(s *eval.Scenario) {
			s.Claims[0].Text = ""
		}},
		{"claim UTF8", func(s *eval.Scenario) {
			s.Claims[0].Text = invalidUTF8
		}},
		{"duplicate claims", func(s *eval.Scenario) {
			s.Claims = append(s.Claims, s.Claims[0])
		}},
		{"no assertions", func(s *eval.Scenario) {
			s.Claims = nil
		}},
		{"partial tools", func(s *eval.Scenario) {
			s.Claims = nil
			s.Items = eval.Items(eval.Item{ID: "one", Input: "hi", ExpectedTools: &expect.ExpectedTools{}}, eval.Item{ID: "two", Input: "hi"})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := validScenario()
			tc.change(&s)
			err := eval.Validate(s)
			require.ErrorContains(t, err, "scenario")
		})
	}
	s := validScenario()
	require.NoError(t, eval.Validate(s))
	require.ErrorContains(t, eval.Validate(s, s), "duplicate scenario ID")
	s.Agent = ""
	s.Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
		return nil, nil
	}
	require.NoError(t, eval.Validate(s))
	s = validScenario()
	s.Claims = nil
	s.Expect = []expect.Expectation{expect.MaxModelCalls(2)}
	require.NoError(t, eval.Validate(s))
	s.Expect = nil
	s.Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{}, nil
	}
	require.NoError(t, eval.Validate(s))
	s.Assert = nil
	s.Items = eval.Items(eval.Item{ID: "one", Input: "hi", ExpectedTools: &expect.ExpectedTools{}})
	require.NoError(t, eval.Validate(s))
}

func TestScenarioValidationJoinsProblems(t *testing.T) {
	s := validScenario()
	s.Timeout = -1
	s.Tags = []string{"bad/tag", "bad/tag"}
	s.Items = eval.Items(eval.Item{ID: "bad/id", Input: "hi"})
	s.Claims = []eval.Claim{{ID: "Upper", Text: ""}}
	err := eval.Validate(s)
	require.ErrorContains(t, err, "Timeout")
	require.ErrorContains(t, err, "tag")
	require.ErrorContains(t, err, `scenario "case" item "bad/id":`)
	require.ErrorContains(t, err, "claim")
	for _, line := range strings.Split(err.Error(), "\n") {
		assert.True(t, strings.HasPrefix(line, `scenario "case"`), line)
	}
}

func TestScenarioDeferredDecodeError(t *testing.T) {
	s := validScenario()
	s.Items = eval.JSONL(fstest.MapFS{"go": &fstest.MapFile{Data: []byte(header + "\n" + row + "\n" + row)}}, "go")
	err := eval.Validate(s)
	var de *eval.DecodeError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "go", de.Source)
	assert.Equal(t, 3, de.Line)
	require.ErrorContains(t, err, `scenario "case":`)
}

func validScenario() eval.Scenario {
	return eval.Scenario{ID: "case", Agent: "agent", Items: eval.Items(eval.Item{ID: "one", Input: "hi"}), Claims: []eval.Claim{{ID: "correct", Text: "Answers correctly"}}}
}
