package eval

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Scenario shares defaults, assertions and optional driving code across items.
// BuildInputs, Drive, Assert and expectation predicates must be safe for concurrent calls.
type Scenario struct {
	// ID uniquely identifies the scenario in a suite.
	ID string
	// Agent names the registered agent used by the default driver.
	Agent agent.Ident
	// Task supplies the default task description when an item omits it.
	Task string
	// Reference supplies the default reference when an item omits it.
	Reference string
	// Tags label the scenario for selection.
	Tags []string
	// Timeout bounds BuildInputs, Drive and Assert; zero uses the caller's context.
	Timeout time.Duration
	// Items contains the immutable dataset executed for this scenario.
	Items Dataset
	// Claims declares judge criteria shared by all items.
	Claims []Claim
	// Expect contains deterministic expectations evaluated against each session.
	Expect []expect.Expectation
	// BuildInputs constructs ordered turns for the standard sequential driver.
	// It receives the resolved item and must honor ctx; it cannot be used with Drive.
	BuildInputs func(ctx context.Context, item Item) ([]agenttest.Input, error)
	// Drive replaces the default sequential RunSession driver when non-nil.
	Drive func(ctx context.Context, h *agenttest.Harness, item Item) (*agenttest.SessionResult, error)
	// Assert adds checks, scores and claims after the declared expectations.
	Assert func(ctx context.Context, a *Attempt) (Assertions, error)
}

// Claim declares a criterion for a judge; Execute does not judge it.
type Claim struct {
	ID            string
	Text          string
	Informational bool
	// Subject defaults to answer. Tool selects calls only for step subjects.
	Subject Subject
	Tool    tools.Ident
}

// Assertions contains additional quality checks, claim declarations and scores.
type Assertions struct {
	Checks []expect.Check
	Claims []Claim
	Scores []expect.Score
}

var claimIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// Validate joins all scenario and item problems with their declaration locations.
func Validate(scenarios ...Scenario) error {
	var problems []error
	seen := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		add := func(err error) {
			problems = append(problems, prefixProblems(fmt.Sprintf("scenario %q: ", s.ID), err)...)
		}
		if !idPattern.MatchString(s.ID) {
			add(errors.New("invalid scenario ID"))
		}
		if seen[s.ID] {
			add(errors.New("duplicate scenario ID"))
		}
		seen[s.ID] = true
		add(validateDriver(s))
		if s.Timeout < 0 {
			add(errors.New("Timeout must be non-negative"))
		}
		if s.Items.fromItems && s.Items.Len() > 0 {
			problems = append(problems, prefixProblems(fmt.Sprintf("scenario %q ", s.ID), s.Items.Err())...)
		} else {
			add(s.Items.Err())
		}
		add(validateTags(s.Tags))
		add(validateClaims(s.Claims))
		allTools := s.Items.Len() > 0
		for _, item := range s.Items.items {
			allTools = allTools && item.ExpectedTools != nil
			task := item.Task
			if task == "" {
				task = s.Task
			}
			problems = append(problems, prefixProblems(fmt.Sprintf("scenario %q item %q: ", s.ID, item.ID), claimPlaceholders(s.Claims, task, item.ExpectedOutcome))...)
		}
		if len(s.Claims) == 0 && len(s.Expect) == 0 && s.Assert == nil && !allTools {
			add(ErrNoAssertions)
		}
	}
	return errors.Join(problems...)
}

// validateDriver keeps standard input building distinct from custom execution.
func validateDriver(s Scenario) error {
	var problems []error
	if s.Agent == "" && s.Drive == nil {
		problems = append(problems, errors.New("Agent is required unless Drive is set"))
	}
	if s.BuildInputs != nil && s.Drive != nil {
		problems = append(problems, errors.New("BuildInputs and Drive cannot both be set"))
	}
	return errors.Join(problems...)
}

func validateTags(tags []string) error {
	var problems []error
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if !idPattern.MatchString(tag) {
			problems = append(problems, fmt.Errorf("invalid tag %q", tag))
		}
		if seen[tag] {
			problems = append(problems, fmt.Errorf("duplicate tag %q", tag))
		}
		seen[tag] = true
	}
	return errors.Join(problems...)
}

func validateClaims(claims []Claim) error {
	var problems []error
	seen := make(map[string]bool, len(claims))
	for _, claim := range claims {
		if !claimIDPattern.MatchString(claim.ID) {
			problems = append(problems, fmt.Errorf("invalid claim ID %q", claim.ID))
		}
		if claim.Text == "" || !utf8.ValidString(claim.Text) {
			problems = append(problems, fmt.Errorf("claim %q text must be non-empty valid UTF-8", claim.ID))
		}
		if seen[claim.ID] {
			problems = append(problems, fmt.Errorf("duplicate claim ID %q", claim.ID))
		}
		seen[claim.ID] = true
		switch claim.Subject {
		case "", SubjectAnswer, SubjectTrajectory, SubjectSession, SubjectStep:
		default:
			problems = append(problems, fmt.Errorf("claim %q has unknown subject %q", claim.ID, claim.Subject))
		}
		if claim.Tool != "" && claim.Subject != SubjectStep {
			problems = append(problems, fmt.Errorf("claim %q Tool requires step subject", claim.ID))
		}
	}
	return errors.Join(problems...)
}
