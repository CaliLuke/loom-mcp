package eval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Attempt binds a resolved item to its harness and session evidence.
type Attempt struct {
	Scenario string
	Item     Item
	Harness  *agenttest.Harness
	Session  *agenttest.SessionResult
}

// Outcome separates quality results from execution and assertion failures.
// Err is an infrastructure error; failed quality checks leave Err nil.
type Outcome struct {
	Scenario  string
	Item      string
	Task      string
	Reference string
	Session   *agenttest.SessionResult
	Checks    []expect.Check
	Scores    []expect.Score
	Claims    []Claim
	Err       error
}

// ErrNoAssertions means no user quality checks or claim declarations were supplied.
// The mandatory runs_succeeded health check does not satisfy this requirement.
var ErrNoAssertions = errors.New("eval: no assertions")

// Execute drives one item and evaluates its session. Only invalid arguments return
// a non-nil error; infrastructure failures are retained in Outcome.Err. Timeout
// covers Drive and Assert, whose callbacks must cooperate with cancellation.
// Callbacks and expectation predicates must be safe for concurrent execution.
func Execute(ctx context.Context, h *agenttest.Harness, s Scenario, item Item) (*Outcome, error) {
	if h == nil {
		return nil, errors.New("eval: nil harness")
	}
	if err := Validate(s); err != nil {
		return nil, err
	}
	if err := validateItem(item); err != nil {
		return nil, fmt.Errorf("scenario %q item %q: %w", s.ID, item.ID, err)
	}
	item = cloneItem(item)
	if item.Task == "" {
		item.Task = s.Task
	}
	if item.Reference == "" {
		item.Reference = s.Reference
	}
	out := &Outcome{Scenario: s.ID, Item: item.ID, Task: item.Task, Reference: item.Reference, Claims: slices.Clone(s.Claims)}
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	session, err := drive(ctx, h, s, item)
	out.Session = session
	if err = errors.Join(err, ctx.Err()); err != nil {
		out.Err = err
		return out, nil
	}
	if session == nil {
		out.Err = errors.New("eval: Drive returned a nil session")
		return out, nil
	}
	attempt := &Attempt{Scenario: s.ID, Item: item, Harness: h, Session: session}
	out.Err = evaluate(ctx, s, attempt, out)
	return out, nil
}

// Subject returns the session trajectory as an expectation subject.
func (a *Attempt) Subject() expect.Subject {
	return expect.Session(a.Session.Trajectory)
}

// Final returns the final message from the last session trajectory, if present.
func (a *Attempt) Final() *model.Message {
	if a.Session == nil || len(a.Session.Trajectory) == 0 {
		return nil
	}
	last := a.Session.Trajectory[len(a.Session.Trajectory)-1]
	if last == nil {
		return nil
	}
	return last.Final
}

// drive recovers callback panics while leaving normal harness errors intact.
func drive(ctx context.Context, h *agenttest.Harness, s Scenario, item Item) (session *agenttest.SessionResult, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("eval: Drive panic: %v\n%s", value, debug.Stack())
		}
	}()
	if s.Drive != nil {
		return s.Drive(ctx, h, item)
	}
	turns := item.Turns
	if item.Input != "" {
		turns = []string{item.Input}
	}
	inputs := make([]agenttest.Input, len(turns))
	for n, turn := range turns {
		inputs[n] = agenttest.User(turn)
	}
	return h.RunSession(ctx, s.Agent, inputs...)
}

// evaluate preserves declared ordering and separates broken assertions from failures.
func evaluate(ctx context.Context, s Scenario, a *Attempt, out *Outcome) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("eval: assertion panic: %v\n%s", value, debug.Stack())
		}
		err = errors.Join(err, ctx.Err())
	}()
	subject := a.Subject()
	out.Checks = append([]expect.Check{runsSucceeded(a.Session)}, expect.Evaluate(subject, s.Expect...)...)
	if a.Item.ExpectedTools != nil {
		check, score := a.Item.ExpectedTools.Evaluate(subject)
		out.Checks = append(out.Checks, check)
		out.Scores = append(out.Scores, score)
	}
	if s.Assert != nil {
		extra, assertErr := s.Assert(ctx, a)
		if assertErr != nil {
			return assertErr
		}
		out.Checks = append(out.Checks, extra.Checks...)
		out.Scores = append(out.Scores, extra.Scores...)
		out.Claims = append(out.Claims, extra.Claims...)
	}
	if err := validateResults(out); err != nil {
		return err
	}
	uniqueNames(out.Checks, out.Scores)
	return nil
}

func runsSucceeded(session *agenttest.SessionResult) expect.Check {
	var failures []string
	for _, run := range session.Runs {
		if run != nil && run.RunErr != nil {
			failures = append(failures, fmt.Sprintf("run %s: %v", run.RunID, run.RunErr))
		}
	}
	return expect.Check{Name: "runs_succeeded", Passed: len(failures) == 0, Diagnostic: strings.Join(failures, "; ")}
}

func validateResults(out *Outcome) error {
	var problems []error
	for _, check := range out.Checks {
		if check.Name == "" {
			problems = append(problems, errors.New("eval: empty check name"))
		}
		if check.Passed && check.Diagnostic != "" {
			problems = append(problems, fmt.Errorf("eval: passed check %q has a diagnostic", check.Name))
		}
		if !check.Passed && check.Diagnostic == "" {
			problems = append(problems, fmt.Errorf("eval: failed check %q has no diagnostic", check.Name))
		}
	}
	for _, score := range out.Scores {
		if score.Name == "" {
			problems = append(problems, errors.New("eval: empty score name"))
		}
		if math.IsNaN(score.Value) || math.IsInf(score.Value, 0) {
			problems = append(problems, fmt.Errorf("eval: score %q must be finite", score.Name))
		}
	}
	problems = append(problems, validateClaims(out.Claims))
	if len(out.Checks) == 1 && len(out.Claims) == 0 {
		problems = append(problems, ErrNoAssertions)
	}
	return errors.Join(problems...)
}

// uniqueNames uses independent namespaces for checks and scores, matching expect.
func uniqueNames(checks []expect.Check, scores []expect.Score) {
	checkNames := make(map[string]bool, len(checks))
	for n := range checks {
		checks[n].Name = uniqueName(checks[n].Name, checkNames)
	}
	scoreNames := make(map[string]bool, len(scores))
	for n := range scores {
		scores[n].Name = uniqueName(scores[n].Name, scoreNames)
	}
}

func uniqueName(base string, seen map[string]bool) string {
	name := base
	for suffix := 2; seen[name]; suffix++ {
		name = fmt.Sprintf("%s#%d", base, suffix)
	}
	seen[name] = true
	return name
}
