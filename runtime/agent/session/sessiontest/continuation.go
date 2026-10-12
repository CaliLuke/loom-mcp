// Package sessiontest provides executable continuation-store conformance checks.
package sessiontest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

// ContinuationCrashHook attempts to publish the supplied body with a store-specific
// fault between the body insert and head update, returning the injected error.
// The hook must disarm the fault before returning so a normal retry can succeed.
// Stores with an indivisible in-memory update can use a cancelled-context attempt.
type ContinuationCrashHook func(context.Context, session.Continuation) error

// CheckContinuation checks ID-first idempotency, body comparison, and head CAS.
// The caller provides a newly created, empty session. An optional crash hook also
// checks rollback and retry publication. Errors identify the broken rule.
func CheckContinuation(ctx context.Context, store session.ContinuationStore, id string, crashHook ...ContinuationCrashHook) error {
	first := session.Continuation{ID: "first", Pos: 1}
	if len(crashHook) > 1 {
		return errors.New("at most one continuation crash hook is allowed")
	}
	firstOutcome := session.Published
	if len(crashHook) == 1 {
		if err := checkContinuationCrash(ctx, store, id, first, crashHook[0]); err != nil {
			return err
		}
		firstOutcome = session.AlreadyPublished
	}
	second := session.Continuation{ID: "second", Prev: new(first.ID), Pos: 2}
	steps := []struct {
		body     session.Continuation
		expected *string
		want     session.AdvanceOutcome
	}{
		{first, nil, firstOutcome},
		{second, new(first.ID), session.Published},
		{first, nil, session.AlreadyPublished},
		{session.Continuation{ID: first.ID, Pos: 2}, nil, session.Conflict},
		{session.Continuation{ID: "late", Pos: 1}, nil, session.Superseded},
	}
	for i, step := range steps {
		got, err := store.AdvanceContinuation(ctx, id, step.expected, step.body)
		if err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
		if got != step.want {
			return fmt.Errorf("step %d: got %s, want %s", i, got, step.want)
		}
		head, err := store.LoadContinuationHead(ctx, id)
		if err != nil {
			return err
		}
		want := second.ID
		if i == 0 {
			want = first.ID
		}
		if head == nil || head.ID != want {
			return fmt.Errorf("step %d: body published without matching head", i)
		}
	}
	return nil
}

// CheckContinuationRounds races publishers from the same head through four
// successive positions. Every round must commit exactly one body and head.
func CheckContinuationRounds(ctx context.Context, store session.ContinuationStore, sessionID string) error {
	var previous *string
	for pos := 1; pos <= 4; pos++ {
		if err := raceContinuationRound(ctx, store, sessionID, previous, pos); err != nil {
			return err
		}
		head, err := checkRoundHead(ctx, store, sessionID, previous, pos)
		if err != nil {
			return err
		}
		previous = new(head.ID)
	}
	return nil
}

// checkContinuationCrash checks both rollback and retry even when the crashed
// attempt leaves an orphan, so an injected error alone cannot prove conformance.
func checkContinuationCrash(ctx context.Context, store session.ContinuationStore, id string, next session.Continuation, crash ContinuationCrashHook) error {
	if err := crash(ctx, next); err == nil {
		return errors.New("crash hook: expected an injected error")
	}
	var failures []error
	head, err := store.LoadContinuationHead(ctx, id)
	if err != nil {
		return fmt.Errorf("after crash: load head: %w", err)
	}
	if head != nil {
		failures = append(failures, errors.New("after crash: head must be nil"))
	}
	_, err = store.LoadContinuation(ctx, id, next.ID)
	if err == nil {
		failures = append(failures, errors.New("after crash: orphaned body"))
	} else if !errors.Is(err, session.ErrContinuationNotFound) {
		return fmt.Errorf("after crash: load body: %w", err)
	}
	outcome, err := store.AdvanceContinuation(ctx, id, nil, next)
	if err != nil {
		failures = append(failures, fmt.Errorf("retry: %w", err))
	} else if outcome != session.Published {
		failures = append(failures, fmt.Errorf("retry: got %s, want %s", outcome, session.Published))
	}
	head, err = store.LoadContinuationHead(ctx, id)
	if err != nil {
		failures = append(failures, fmt.Errorf("retry: load head: %w", err))
	} else if head == nil || head.ID != next.ID || head.Pos != next.Pos {
		failures = append(failures, errors.New("retry: body published without matching head"))
	}
	return errors.Join(failures...)
}

func raceContinuationRound(ctx context.Context, store session.ContinuationStore, sessionID string, previous *string, pos int) error {
	type result struct {
		outcome session.AdvanceOutcome
		err     error
	}
	results := make(chan result, 8)
	var workers sync.WaitGroup
	for contender := range 8 {
		next := session.Continuation{ID: fmt.Sprintf("round-%d-%d", pos, contender), Prev: previous, Pos: pos}
		workers.Go(func() {
			outcome, err := store.AdvanceContinuation(ctx, sessionID, previous, next)
			results <- result{outcome, err}
		})
	}
	workers.Wait()
	close(results)
	published := 0
	for result := range results {
		if result.err != nil {
			return result.err
		}
		if result.outcome == session.Published {
			published++
		} else if result.outcome != session.Superseded {
			return fmt.Errorf("round %d: unexpected %s", pos, result.outcome)
		}
	}
	if published != 1 {
		return fmt.Errorf("round %d: %d successors", pos, published)
	}
	return nil
}

func checkRoundHead(ctx context.Context, store session.ContinuationStore, sessionID string, previous *string, pos int) (*session.Continuation, error) {
	head, err := store.LoadContinuationHead(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if head == nil || head.Pos != pos || (previous == nil) != (head.Prev == nil) || (previous != nil && *previous != *head.Prev) {
		return nil, fmt.Errorf("round %d: invalid head %+v", pos, head)
	}
	body, err := store.LoadContinuation(ctx, sessionID, head.ID)
	if err != nil {
		return nil, err
	}
	if body.Pos != pos || body.ID != head.ID {
		return nil, fmt.Errorf("round %d: invalid stored body %+v", pos, body)
	}
	return head, nil
}
