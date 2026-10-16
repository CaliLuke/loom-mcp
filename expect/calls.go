package expect

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

type callsSubject struct {
	calls []model.ToolCall
}

type callExpectation struct {
	name              string
	tool              tools.Ident
	match             func([]byte) error
	execution         func(*trajectory.ToolStep) error
	failedOnly        bool
	verifiedExecution bool
}

type forbidExpectation struct {
	tools []tools.Ident
}

type exactExpectation struct {
	expectations []Expectation
}

// Calls constructs a subject from the ordered calls chosen by a model. Calls
// provide arguments but no execution results, errors, or run-level evidence.
func Calls(calls []model.ToolCall) Subject {
	return &callsSubject{calls: calls}
}

// Call matches the first later call satisfying the typed predicates. Either
// predicate may be nil. Nil predicates skip decoding that value. A result
// predicate is not applicable to a Calls subject.
func Call[P, R any](tool tools.TypedTool[P, R], args func(P) error, result func(R) error) Expectation {
	var match func([]byte) error
	if args != nil {
		match = decodePredicate(tool.Payload, args, "arguments")
	}
	var execution func(*trajectory.ToolStep) error
	if result != nil {
		decode := decodePredicate(tool.Result, result, "result")
		execution = func(step *trajectory.ToolStep) error {
			if step.Error != nil {
				return fmt.Errorf("tool failed: %s", step.Error.Message)
			}
			if len(step.Result) == 0 {
				return fmt.Errorf("no recorded result")
			}
			return decode(step.Result)
		}
	}
	return callExpectation{name: "call " + tool.Name.String(), tool: tool.Name, match: match, execution: execution}
}

// Decoded matches the first later named call whose arguments decode with codec
// and satisfy args. A nil predicate still requires successful decoding.
func Decoded[T any](tool tools.Named, codec tools.JSONCodec[T], args func(T) error) Expectation {
	return callExpectation{
		name:  "decoded " + tool.Ident().String(),
		tool:  tool.Ident(),
		match: decodePredicate(codec, args, "arguments"),
	}
}

// Forbid rejects calls to any of the named tools anywhere in the subject.
func Forbid(names ...tools.Named) Expectation {
	ids := make([]tools.Ident, len(names))
	for i, name := range names {
		ids[i] = name.Ident()
	}
	return forbidExpectation{tools: ids}
}

// Exact requires its expectations to match the remaining calls one-to-one in
// order. It panics at construction if any nested expectation is Forbid.
func Exact(expectations ...Expectation) Expectation {
	for _, expectation := range expectations {
		if forbidden, ok := expectation.(interface {
			containsForbid() bool
		}); ok && forbidden.containsForbid() {
			panic("expect: Exact cannot contain Forbid")
		}
	}
	return exactExpectation{expectations: append([]Expectation(nil), expectations...)}
}

// decodePredicate quotes codec errors so the original message is identifiable
// separately from an argument predicate's diagnostic.
func decodePredicate[T any](codec tools.JSONCodec[T], predicate func(T) error, valueKind string) func([]byte) error {
	return func(data []byte) error {
		value, err := codec.FromJSON(data)
		if err != nil {
			return fmt.Errorf("decode %s: %q", valueKind, err.Error())
		}
		if predicate != nil {
			return predicate(value)
		}
		return nil
	}
}

func (s *callsSubject) normalize() *subject {
	normalized := &subject{source: s, modelCall: true, calls: make([]normalizedCall, len(s.calls))}
	for i, call := range s.calls {
		normalized.calls[i] = normalizedCall{ToolCall: call}
	}
	return normalized
}

func (e callExpectation) eval(s *subject) Check {
	check := Check{Name: e.name}
	if s.modelCall && e.execution != nil {
		check.Diagnostic = notApplicable
		return check
	}
	check.Diagnostic = "no matching call to " + e.tool.String()
	successMatched, failureSeen := false, false
	for i := s.cursor; i < len(s.calls); i++ {
		candidate := s.calls[i]
		if s.exact {
			s.cursor = i + 1
		}
		if candidate.Name == e.tool {
			err, successful := e.matchCall(candidate, s.exact)
			if successful {
				if err == nil {
					successMatched = true
				} else if !failureSeen {
					check.Diagnostic = fmt.Sprintf("call %d to %s: %s", i+1, e.tool, err.Error())
				}
				continue
			}
			if e.failedOnly {
				failureSeen = true
			}
			if err != nil {
				check.Diagnostic = fmt.Sprintf("call %d to %s: %s", i+1, e.tool, err.Error())
			} else {
				s.cursor = i + 1
				return Check{Name: e.name, Passed: true}
			}
		}
		if s.exact {
			break
		}
	}
	if successMatched && !failureSeen {
		check.Diagnostic = "call to " + e.tool.String() + " did not fail"
	}
	return check
}

func (e forbidExpectation) eval(s *subject) Check {
	for _, candidate := range s.calls {
		for _, name := range e.tools {
			if candidate.Name == name {
				return Check{Name: "forbid", Diagnostic: "forbidden call to " + name.String()}
			}
		}
	}
	return Check{Name: "forbid", Passed: true}
}

func (e forbidExpectation) containsForbid() bool {
	return true
}

func (e exactExpectation) eval(s *subject) Check {
	previous := s.exact
	s.exact = true
	defer func() {
		s.exact = previous
	}()
	var failures []string
	for _, expectation := range e.expectations {
		check := validateCheck(expectation.eval(s))
		if !check.Passed {
			failures = append(failures, check.Name+": "+check.Diagnostic)
		}
	}
	if s.cursor != len(s.calls) {
		failures = append(failures, fmt.Sprintf("%d unmatched calls", len(s.calls)-s.cursor))
	}
	return Check{Name: "exact", Passed: len(failures) == 0, Diagnostic: strings.Join(failures, "; ")}
}

// matchCall reports successful executions separately for failure subsequences,
// so they cannot hide a failed execution's diagnostic. Exact remains positional.
func (e callExpectation) matchCall(call normalizedCall, exact bool) (error, bool) {
	if e.verifiedExecution && call.execution != nil && !call.verified {
		return fmt.Errorf("evidence unverified: execution"), false
	}
	var err error
	if e.match != nil {
		err = e.match(call.Payload)
	}
	if e.failedOnly && !exact && call.execution != nil && call.execution.Error == nil {
		return err, true
	}
	if err != nil || e.execution == nil {
		return err, false
	}
	if call.execution == nil {
		return fmt.Errorf("no recorded execution"), false
	}
	return e.execution(call.execution), false
}
