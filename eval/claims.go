package eval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Subject selects the evidence evaluated for a claim.
type Subject string

const (
	// SubjectAnswer judges the last run's final message.
	SubjectAnswer Subject = "answer"
	// SubjectTrajectory judges the last run, including its children.
	SubjectTrajectory Subject = "trajectory"
	// SubjectSession judges every run in the conversation.
	SubjectSession Subject = "session"
	// SubjectStep judges each matching root-run call against its preceding context.
	SubjectStep Subject = "step"
)

// TaskCompletion judges whether the trajectory completed the item's task.
func TaskCompletion() Claim {
	return Claim{ID: "task_completion", Subject: SubjectTrajectory, Text: "The agent completed the task: {task}."}
}

// StepEfficiency judges whether the trajectory used unnecessary steps.
func StepEfficiency() Claim {
	return Claim{ID: "step_efficiency", Subject: SubjectTrajectory, Text: "The agent took no unnecessary steps for the task."}
}

// ConversationCompleteness judges the conversation's expected outcome.
func ConversationCompleteness() Claim {
	return Claim{ID: "conversation_completeness", Subject: SubjectSession, Text: "The conversation achieved the expected outcome: {expected_outcome}."}
}

// ArgumentCorrectness judges each adopted model call that chose tools.
func ArgumentCorrectness() Claim {
	return Claim{ID: "argument_correctness", Subject: SubjectStep, Text: "The arguments of this call are correct given the conversation so far."}
}

// ToolArgumentCorrectness judges calls to t. Its ID is lowercase ASCII, with
// non-alphanumeric characters replaced by underscores, truncated to 64 bytes.
// Validate rejects collisions between sanitized IDs.
func ToolArgumentCorrectness(t tools.Named) Claim {
	c := ArgumentCorrectness()
	c.Tool = t.Ident()
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, strings.ToLower(string(c.Tool)))
	c.ID += "_" + name
	c.ID = c.ID[:min(len(c.ID), 64)]
	return c
}

// claimPlaceholders validates only the two reserved placeholders before expansion.
func claimPlaceholders(claims []Claim, task, expected string) error {
	var problems []error
	for _, c := range claims {
		if strings.Contains(c.Text, "{task}") && task == "" {
			problems = append(problems, fmt.Errorf("claim %q requires {task}", c.ID))
		}
		if strings.Contains(c.Text, "{expected_outcome}") && expected == "" {
			problems = append(problems, fmt.Errorf("claim %q requires {expected_outcome}", c.ID))
		}
	}
	return errors.Join(problems...)
}

func resolveClaims(claims []Claim, item Item) error {
	if err := claimPlaceholders(claims, item.Task, item.ExpectedOutcome); err != nil {
		return err
	}
	replace := strings.NewReplacer("{task}", item.Task, "{expected_outcome}", item.ExpectedOutcome)
	for i := range claims {
		claims[i].Text = replace.Replace(claims[i].Text)
		if claims[i].Subject == "" {
			claims[i].Subject = SubjectAnswer
		}
	}
	return nil
}
