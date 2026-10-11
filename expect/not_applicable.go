package expect

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

type runExpectation struct {
	name      string
	predicate func(*subject) error
}

const notApplicable = "not applicable to a model-call subject"

// Failed matches the first later failed call satisfying optional argument and
// error predicates. It is not applicable to Calls.
func Failed[P, R any](tool tools.TypedTool[P, R], args func(P) error, failure func(*planner.ToolError) error) Expectation {
	var match func([]byte) error
	if args != nil {
		match = decodePredicate(tool.Payload, args, "arguments")
	}
	return callExpectation{name: "failed " + tool.Name.String(), tool: tool.Name, match: match, failedOnly: true, execution: func(step *trajectory.ToolStep) error {
		if step.Error == nil {
			return fmt.Errorf("call to %s did not fail", tool.Name)
		}
		if failure != nil {
			return failure(step.Error)
		}
		return nil
	}}
}

// Bounds checks recorded bounds on any execution of tool. It does not consume
// the call-matching cursor. It is not applicable to Calls.
func Bounds(tool tools.Named, predicate func(*agent.Bounds) error) Expectation {
	return runExpectation{name: "bounds " + tool.Ident().String(), predicate: func(s *subject) error {
		diagnostic := fmt.Errorf("no recorded bounds for %s", tool.Ident())
		for _, call := range s.calls {
			if call.Name != tool.Ident() || call.execution.Bounds == nil {
				continue
			}
			if predicate == nil {
				return nil
			}
			if err := predicate(call.execution.Bounds); err != nil {
				diagnostic = err
			} else {
				return nil
			}
		}
		return diagnostic
	}}
}

// Completed checks that every selected run has observed successful completion.
// Missing or unverified completion evidence fails. It is not applicable to Calls.
func Completed() Expectation {
	return runExpectation{name: "completed", predicate: func(s *subject) error {
		if len(s.runs) == 0 {
			return fmt.Errorf("no runs to check for completion")
		}
		for _, run := range s.runs {
			if !run.StatusVerified {
				return fmt.Errorf("evidence unverified: status")
			}
			if run.Status != "success" {
				return fmt.Errorf("run %s did not complete successfully (status %q)", run.RunID, run.Status)
			}
		}
		return nil
	}}
}

// AllAttemptsSuccessful checks every selected execution of tool, including
// earlier failed calls before a later successful call. No calls fails the check.
// It is not applicable to Calls.
func AllAttemptsSuccessful(tool tools.Named) Expectation {
	return runExpectation{name: "all_attempts_successful " + tool.Ident().String(), predicate: func(s *subject) error {
		called := false
		for _, call := range s.calls {
			if call.Name != tool.Ident() {
				continue
			}
			called = true
			if call.execution.Error != nil {
				return fmt.Errorf("call %s failed: %s", call.ID, call.execution.Error.Message)
			}
			if len(call.execution.Result) == 0 {
				return fmt.Errorf("call %s has no recorded successful result", call.ID)
			}
		}
		if !called {
			return fmt.Errorf("no call to %s", tool.Ident())
		}
		return nil
	}}
}

// MaxModelCalls limits adopted model calls across every selected run. Abandoned
// and incomplete calls do not count. It is not applicable to Calls.
func MaxModelCalls(n int) Expectation {
	return runExpectation{name: "max_model_calls", predicate: func(s *subject) error {
		count := 0
		for _, run := range s.runs {
			count += run.Totals.AdoptedModelCalls
		}
		if count > n {
			return fmt.Errorf("%d adopted model calls exceeds limit %d", count, n)
		}
		return nil
	}}
}

// MaxTokens limits input, output and cache tokens from all recorded model calls,
// including abandoned calls, across selected runs. It is not applicable to Calls.
func MaxTokens(n int) Expectation {
	return runExpectation{name: "max_tokens", predicate: func(s *subject) error {
		count := 0
		for _, run := range s.runs {
			for _, usage := range run.Totals.TokensByModel {
				count += usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
			}
		}
		if count > n {
			return fmt.Errorf("%d tokens exceeds limit %d", count, n)
		}
		return nil
	}}
}

func (e runExpectation) eval(s *subject) Check {
	check := Check{Name: e.name}
	if s.modelCall {
		check.Diagnostic = notApplicable
		return check
	}
	if err := e.predicate(s); err != nil {
		check.Diagnostic = err.Error()
		return check
	}
	check.Passed = true
	return check
}
