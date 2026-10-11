package expect

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ExpectedTool is a tool identifier with optional JSON-subset arguments.
// Omitted Args matches any arguments, while JSON null requires an actual null.
type ExpectedTool struct {
	Name tools.Ident     `json:"name"`
	Args json.RawMessage `json:"args,omitzero"`
}

// ExpectedTools describes data-driven tool correctness. Ordered uses greedy
// subsequence matching; otherwise matching is unordered and one-to-one. Exact
// additionally requires every actual call to match an expected call.
type ExpectedTools struct {
	Tools   []ExpectedTool `json:"tools"`
	Ordered bool           `json:"ordered"`
	Exact   bool           `json:"exact"`
}

// Evaluate returns the tool check and its tool_correctness score: matching calls
// divided by calls made. With no calls, the score is one if none were expected,
// otherwise zero. The score has no threshold. Objects match recursively as
// subsets, arrays match exactly, and numbers compare by exact decimal value.
// Malformed actual arguments are non-matches reported in a failed check's
// diagnostic, while valid calls still contribute to the score.
func (e ExpectedTools) Evaluate(s Subject) (Check, Score) {
	return e.evaluate(s.normalize())
}

// toolEdges computes name-and-argument compatibility without numeric rounding.
func toolEdges(expected []ExpectedTool, calls []normalizedCall) ([][]bool, []string, error) {
	actual := make([]any, len(calls))
	valid := make([]bool, len(calls))
	var diagnostics []string
	for i, call := range calls {
		value, err := decodeJSON(call.Payload)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("call %d arguments for %s: %q", i+1, call.Name, err.Error()))
			continue
		}
		actual[i], valid[i] = value, true
	}
	edges := make([][]bool, len(expected))
	for i, tool := range expected {
		var args any
		if len(tool.Args) != 0 {
			value, err := decodeJSON(tool.Args)
			if err != nil {
				return nil, diagnostics, fmt.Errorf("expected arguments for %s: %q", tool.Name, err.Error())
			}
			args = value
		}
		edges[i] = make([]bool, len(calls))
		for j, call := range calls {
			if tool.Name != call.Name || !valid[j] {
				continue
			}
			if len(tool.Args) == 0 {
				edges[i][j] = true
				continue
			}
			edges[i][j] = jsonSubset(args, actual[j], false)
		}
	}
	return edges, diagnostics, nil
}

// matchExpected uses greedy ordered matching or maximum unordered matching.
// Augmenting paths prevent a broad expectation from stealing the only call
// satisfying a more specific expectation.
func matchExpected(edges [][]bool, made int, ordered bool) int {
	matched := 0
	cursor := 0
	owners := make([]int, made)
	for i := range owners {
		owners[i] = -1
	}
	for i, candidates := range edges {
		if ordered {
			for j := cursor; j < made; j++ {
				if candidates[j] {
					cursor = j + 1
					matched++
					break
				}
			}
		} else if bindExpected(edges, owners, make([]bool, made), i) {
			matched++
		}
	}
	return matched
}

func bindExpected(edges [][]bool, owners []int, seen []bool, expected int) bool {
	for actual, matches := range edges[expected] {
		if !matches || seen[actual] {
			continue
		}
		seen[actual] = true
		if owners[actual] == -1 || bindExpected(edges, owners, seen, owners[actual]) {
			owners[actual] = expected
			return true
		}
	}
	return false
}

func (e ExpectedTools) eval(s *subject) Check {
	check, _ := e.evaluate(s)
	return check
}

func (e ExpectedTools) evaluate(s *subject) (Check, Score) {
	score := Score{Name: "tool_correctness"}
	check := Check{Name: "expected_tools"}
	edges, diagnostics, err := toolEdges(e.Tools, s.calls)
	if err != nil {
		check.Diagnostic = err.Error()
		return check, score
	}
	matched := matchExpected(edges, len(s.calls), e.Ordered)
	if len(s.calls) == 0 {
		if len(e.Tools) == 0 {
			score.Value = 1
		}
	} else {
		score.Value = float64(matched) / float64(len(s.calls))
	}
	check.Passed = matched == len(e.Tools) && (!e.Exact || len(e.Tools) == len(s.calls)) && len(diagnostics) == 0
	if !check.Passed {
		diagnostics = append(diagnostics, fmt.Sprintf("matched %d of %d expected tools across %d calls (ordered=%t, exact=%t)", matched, len(e.Tools), len(s.calls), e.Ordered, e.Exact))
		check.Diagnostic = strings.Join(diagnostics, "; ")
	}
	return check, score
}
