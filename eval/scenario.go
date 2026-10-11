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
)

// Scenario shares defaults, assertions and optional driving code across items.
// Drive, Assert and expectation predicates must be safe for concurrent calls.
type Scenario struct {
	ID        string
	Agent     agent.Ident
	Task      string
	Reference string
	Tags      []string
	Timeout   time.Duration
	Items     Dataset
	Claims    []Claim
	Expect    []expect.Expectation
	Drive     func(ctx context.Context, h *agenttest.Harness, item Item) (*agenttest.SessionResult, error)
	Assert    func(ctx context.Context, a *Attempt) (Assertions, error)
}

// Claim declares a criterion for a future judge; Execute does not judge it.
type Claim struct {
	ID            string
	Text          string
	Informational bool
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
		if s.Agent == "" && s.Drive == nil {
			add(errors.New("Agent is required unless Drive is set"))
		}
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
		}
		if len(s.Claims) == 0 && len(s.Expect) == 0 && s.Assert == nil && !allTools {
			add(ErrNoAssertions)
		}
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
	}
	return errors.Join(problems...)
}
