package expect

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type invalidExpectation struct{ check Check }
type fatalRecorder struct {
	testing.TB
	messages []string
}

func TestFunc(t *testing.T) {
	s := Calls(nil)
	var received Subject
	checks := Evaluate(s,
		Func("pass", func(got Subject) error {
			received = got
			return nil
		}),
		Func("fail", func(Subject) error {
			return errors.New("predicate failure")
		}),
	)
	assert.Same(t, s, received)
	assert.Equal(t, []Check{{Name: "pass", Passed: true}, {Name: "fail", Diagnostic: "predicate failure"}}, checks)
	assert.Empty(t, Evaluate(s))
}

func TestCheckContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check Check
	}{
		{"passed with diagnostic", Check{Name: "bad", Passed: true, Diagnostic: "unexpected"}},
		{"failed without diagnostic", Check{Name: "bad"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := Evaluate(Calls(nil), invalidExpectation{tc.check})
			require.Len(t, checks, 1)
			assert.False(t, checks[0].Passed)
			assert.Contains(t, checks[0].Diagnostic, "invalid check")
		})
	}
}

func TestUniqueNames(t *testing.T) {
	pass := func(Subject) error {
		return nil
	}
	checks := Evaluate(Calls(nil), Func("same", pass), Func("same", pass), Func("same#2", pass), Func("same", pass))
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name)
	}
	assert.Equal(t, []string{"same", "same#2", "same#2#2", "same#3"}, names)
}

func TestRequire(t *testing.T) {
	for _, tc := range []struct {
		name         string
		expectations []Expectation
		diagnostic   string
	}{
		{"none", nil, "no expectations"},
		{"pass", []Expectation{Func("pass", func(Subject) error {
			return nil
		})}, ""},
		{"failure", []Expectation{
			Func("first", func(Subject) error {
				return errors.New("first diagnostic")
			}),
			Func("pass", func(Subject) error {
				return nil
			}),
			Func("last", func(Subject) error {
				return errors.New("last diagnostic")
			}),
		}, "first diagnostic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &fatalRecorder{}
			invokeRequire(recorder, tc.expectations)
			if tc.diagnostic == "" {
				assert.Empty(t, recorder.messages)
				return
			}
			require.Len(t, recorder.messages, 1)
			assert.Contains(t, recorder.messages[0], tc.diagnostic)
			if tc.name == "failure" {
				message := recorder.messages[0]
				assert.Contains(t, message, "last diagnostic")
				assert.Less(t, strings.Index(message, "first diagnostic"), strings.Index(message, "last diagnostic"))
			}
		})
	}
}

func invokeRequire(t *fatalRecorder, expectations []Expectation) {
	defer func() {
		if recovered := recover(); recovered != nil && recovered != t {
			panic(recovered)
		}
	}()
	Require(t, Calls(nil), expectations...)
}

func (e invalidExpectation) eval(*subject) Check {
	return e.check
}
func (r *fatalRecorder) Helper() {
}
func (r *fatalRecorder) Fatal(args ...any) {
	r.messages = append(r.messages, fmt.Sprint(args...))
	panic(r)
}
