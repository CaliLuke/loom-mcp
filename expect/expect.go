// Package expect checks tool calls against typed expectations.
package expect

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// Check is the result of one expectation. Passed checks have no diagnostic;
// failed checks always have a diagnostic.
type Check struct {
	Name       string
	Passed     bool
	Diagnostic string
}

// Score is a numeric evaluation result with an optional passing threshold.
type Score struct {
	Name      string
	Value     float64
	Threshold *float64
}

// Subject is a sealed input built by a subject constructor, such as Calls.
// Constructors normalize their inputs inside this package.
type Subject interface {
	normalize() *subject
}

// Expectation is a sealed check constructed by this package.
type Expectation interface {
	eval(*subject) Check
}

type subject struct {
	source    Subject
	calls     []normalizedCall
	modelCall bool
	roots     trajectory.Session
	runs      trajectory.Session
	events    []normalizedEvent
	cursor    int
	exact     bool
}

type functionExpectation struct {
	name      string
	predicate func(Subject) error
}

// Func creates an expectation from a named predicate. A nil error passes.
func Func(name string, predicate func(Subject) error) Expectation {
	return functionExpectation{name: name, predicate: predicate}
}

// Evaluate returns one check per expectation in declaration order. Call
// expectations share a greedy, forward-only cursor. Check names are unique;
// repeated names receive #2, #3 and so on, avoiding existing suffixes.
func Evaluate(s Subject, expectations ...Expectation) []Check {
	normalized := s.normalize()
	checks := make([]Check, 0, len(expectations))
	names := make(map[string]bool, len(expectations))
	for _, expectation := range expectations {
		check := validateCheck(expectation.eval(normalized))
		base := check.Name
		for suffix := 2; names[check.Name]; suffix++ {
			check.Name = fmt.Sprintf("%s#%d", base, suffix)
		}
		names[check.Name] = true
		checks = append(checks, check)
	}
	return checks
}

// Require fails a test once with every failed diagnostic in declaration order.
// Requiring no expectations fails the test.
func Require(t testing.TB, s Subject, expectations ...Expectation) {
	t.Helper()
	if len(expectations) == 0 {
		t.Fatal("expect: no expectations")
		return
	}
	var failures []string
	for _, check := range Evaluate(s, expectations...) {
		if !check.Passed {
			failures = append(failures, check.Name+": "+check.Diagnostic)
		}
	}
	if len(failures) != 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
}

// validateCheck detects broken expectation implementations rather than silently
// accepting a contradictory result.
func validateCheck(check Check) Check {
	if check.Passed && check.Diagnostic != "" {
		check.Passed = false
		check.Diagnostic = "invalid check: passed check has a diagnostic: " + check.Diagnostic
	} else if !check.Passed && check.Diagnostic == "" {
		check.Diagnostic = "invalid check: failed check has no diagnostic"
	}
	return check
}

func (e functionExpectation) eval(s *subject) Check {
	check := Check{Name: e.name, Passed: true}
	if err := e.predicate(s.source); err != nil {
		check.Passed = false
		check.Diagnostic = err.Error()
	}
	return check
}
