package runtime

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// ContextOverflowError explains a planner step's authoritative provider failure.
type ContextOverflowError struct {
	Reason       string
	Planner      agent.Ident
	Calls        []runstore.CallKey
	Facts        contextmeasure.RequestFacts
	FirstMessage index.MessageID
	FixedTokens  int
	Shortfall    *compaction.BudgetShortfallError
	Cause        error `json:"-"`
}

var (
	// ErrContextBudget rejects an advisory pre-send guard check.
	ErrContextBudget = errors.New("model request exceeds the context guard budget")
	// ErrContextOverflowPending prevents later calls in a marked attempt.
	ErrContextOverflowPending = errors.New("context overflow recovery is pending")
	// ErrContextOverflowNoProgress reports a request that recovery cannot shrink.
	ErrContextOverflowNoProgress = errors.New("context overflow recovery made no progress")
	// ErrContextBudgetExceeded reports an overflow in the minimal view.
	ErrContextBudgetExceeded = errors.New("minimal context exceeds the model budget")
	// ErrContextOverflowBudget reports three provider overflows in one planner step.
	ErrContextOverflowBudget = errors.New("context overflow recovery budget exhausted")
)

// Error names the planner and the recorded calls explaining the failure.
func (e *ContextOverflowError) Error() string {
	if e.Reason == overflowReasonMinimal {
		return fmt.Sprintf("%v: planner %q, first message %d, fixed overhead %d tokens, calls %v", e.category(), e.Planner, e.FirstMessage, e.FixedTokens, e.Calls)
	}
	text := fmt.Sprintf("%v: planner %q, reason %s, calls %v", e.category(), e.Planner, e.Reason, e.Calls)
	if e.Shortfall != nil {
		text += ": " + e.Shortfall.Error()
	}
	if e.Cause != nil {
		text += ": " + e.Cause.Error()
	}
	return text
}

// Unwrap preserves the distinct category and recorded provider cause.
func (e *ContextOverflowError) Unwrap() []error {
	errors := []error{e.category()}
	if e.Cause != nil {
		errors = append(errors, e.Cause)
	}
	if e.Shortfall != nil {
		errors = append(errors, e.Shortfall)
	}
	return errors
}

func (e *ContextOverflowError) category() error {
	switch e.Reason {
	case overflowReasonMinimal, "budget_too_small":
		return ErrContextBudgetExceeded
	case "budget":
		return ErrContextOverflowBudget
	default:
		return ErrContextOverflowNoProgress
	}
}
