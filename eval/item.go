package eval

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/expect"
)

// Item is a single input or scripted conversation with data-driven expectations.
// Persona is reserved for simulated users and must be empty in version 1.
type Item struct {
	ID              string                `json:"id"`
	Input           string                `json:"input,omitempty"`
	Turns           []string              `json:"turns,omitempty"`
	Task            string                `json:"task,omitempty"`
	ExpectedOutcome string                `json:"expected_outcome,omitempty"`
	Persona         string                `json:"persona,omitempty"`
	Reference       string                `json:"reference,omitempty"`
	ExpectedTools   *expect.ExpectedTools `json:"expected_tools,omitempty"`
	Data            json.RawMessage       `json:"data,omitzero"`
}

type prefixedProblem struct {
	prefix string
	err    error
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Error locates every diagnostic line while Unwrap preserves the original cause.
func (e *prefixedProblem) Error() string {
	return e.prefix + strings.ReplaceAll(e.err.Error(), "\n", "\n"+e.prefix)
}

// Unwrap retains typed errors and sentinel causes under the diagnostic prefix.
func (e *prefixedProblem) Unwrap() error {
	return e.err
}

// validateItem collects all item problems without changing the caller's value.
func validateItem(item Item) error {
	var problems []error
	if !idPattern.MatchString(item.ID) {
		problems = append(problems, errors.New("ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}"))
	}
	if (item.Input != "") == (len(item.Turns) > 0) {
		problems = append(problems, errors.New("exactly one of Input or Turns is required"))
	}
	for n, turn := range item.Turns {
		if turn == "" {
			problems = append(problems, fmt.Errorf("turn %d is empty", n+1))
		}
	}
	if item.Persona != "" {
		problems = append(problems, errors.New("Persona is reserved for simulated users"))
	}
	if item.Input != "" && item.ExpectedOutcome != "" {
		problems = append(problems, errors.New("ExpectedOutcome requires Turns"))
	}
	fields := append([]string{item.ID, item.Input, item.Task, item.ExpectedOutcome, item.Persona, item.Reference}, item.Turns...)
	for _, value := range fields {
		if !utf8.ValidString(value) {
			problems = append(problems, errors.New("every string must be valid UTF-8"))
		}
	}
	if item.Data != nil {
		if err := validRaw(item.Data); err != nil {
			problems = append(problems, fmt.Errorf("Data: %w", err))
		}
	}
	if item.ExpectedTools != nil {
		problems = append(problems, validateTools(item.ExpectedTools))
	}
	return errors.Join(problems...)
}

func validateTools(expected *expect.ExpectedTools) error {
	var problems []error
	for n, tool := range expected.Tools {
		if tool.Name == "" || !utf8.ValidString(string(tool.Name)) {
			problems = append(problems, fmt.Errorf("expected tool %d name must be non-empty valid UTF-8", n+1))
		}
		if tool.Args != nil {
			if err := validRaw(tool.Args); err != nil {
				problems = append(problems, fmt.Errorf("expected tool %d args: %w", n+1, err))
			}
		}
	}
	return errors.Join(problems...)
}

func validRaw(raw []byte) error {
	value := jsontext.Value(slices.Clone(raw))
	return value.Format()
}

// cloneItem copies every mutable member at the dataset ownership boundary.
func cloneItem(item Item) Item {
	item.Turns = slices.Clone(item.Turns)
	item.Data = slices.Clone(item.Data)
	if item.ExpectedTools != nil {
		expected := *item.ExpectedTools
		expected.Tools = slices.Clone(expected.Tools)
		for n := range expected.Tools {
			expected.Tools[n].Args = slices.Clone(expected.Tools[n].Args)
		}
		item.ExpectedTools = &expected
	}
	return item
}

// prefixProblems gives each joined validation problem its own location.
func prefixProblems(prefix string, err error) []error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var problems []error
		for _, cause := range joined.Unwrap() {
			problems = append(problems, prefixProblems(prefix, cause)...)
		}
		return problems
	}
	return []error{&prefixedProblem{prefix: prefix, err: err}}
}
