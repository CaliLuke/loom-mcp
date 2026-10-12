package sessiontest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brokenStore struct {
	session.ContinuationStore
	fault  string
	bodies map[string]session.Continuation
	heads  map[string]session.Continuation
	crash  bool
}

func TestContinuationConformanceDetectsBrokenStores(t *testing.T) {
	for _, fault := range []string{"none", "non_idempotent", "already_without_compare", "unconditional_head", "two_writes"} {
		t.Run(fault, func(t *testing.T) {
			store := inmem.New()
			_, err := store.CreateSession(t.Context(), "s", time.Now())
			require.NoError(t, err)
			broken := newBrokenStore(store, fault)
			var crashHooks []ContinuationCrashHook
			if fault == "two_writes" {
				crashHooks = append(crashHooks, func(ctx context.Context, next session.Continuation) error {
					broken.crash = true
					_, crashErr := broken.AdvanceContinuation(ctx, "s", nil, next)
					broken.crash = false
					return crashErr
				})
			}
			err = CheckContinuation(t.Context(), broken, "s", crashHooks...)
			if fault == "none" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				if fault == "two_writes" {
					require.ErrorContains(t, err, "orphaned body")
					require.ErrorContains(t, err, "retry: got already, want published")
					require.ErrorContains(t, err, "retry: body published without matching head")
					assert.NotContains(t, err.Error(), "crash between body and head")
				}
			}
		})
	}
}

func TestContinuationConformanceCrashRetryInMemory(t *testing.T) {
	store := inmem.New()
	_, err := store.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	err = CheckContinuation(t.Context(), store, "s", func(ctx context.Context, next session.Continuation) error {
		// Memory publication has no fallible operation between its two assignments.
		// Cancellation exercises the failed attempt before that atomic update.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, crashErr := store.AdvanceContinuation(cancelled, "s", nil, next)
		return crashErr
	})
	assert.NoError(t, err)
}

func TestContinuationConcurrentRounds(t *testing.T) {
	store := inmem.New()
	_, err := store.CreateSession(t.Context(), "s", time.Now())
	require.NoError(t, err)
	require.NoError(t, CheckContinuationRounds(t.Context(), store, "s"))
}

func (s *brokenStore) AdvanceContinuation(ctx context.Context, id string, expected *string, next session.Continuation) (session.AdvanceOutcome, error) {
	if s.fault == "two_writes" {
		key := id + "\x00" + next.ID
		if _, exists := s.bodies[key]; exists {
			return session.AlreadyPublished, nil
		}
		s.bodies[key] = next
		if s.crash {
			return "", errors.New("crash between body and head")
		}
		s.heads[id] = next
		return session.Published, nil
	}
	if s.fault == "non_idempotent" {
		head, err := s.LoadContinuationHead(ctx, id)
		if err != nil {
			return "", err
		}
		if head != nil && (expected == nil || *expected != head.ID) {
			return session.Superseded, nil
		}
	}
	got, err := s.ContinuationStore.AdvanceContinuation(ctx, id, expected, next)
	if s.fault == "already_without_compare" && got == session.Conflict {
		got = session.AlreadyPublished
	}
	if s.fault == "unconditional_head" && got == session.Superseded {
		got = session.Published
	}
	return got, err
}

func (s *brokenStore) LoadContinuation(ctx context.Context, sessionID, id string) (session.Continuation, error) {
	if s.fault != "two_writes" {
		return s.ContinuationStore.LoadContinuation(ctx, sessionID, id)
	}
	if body, exists := s.bodies[sessionID+"\x00"+id]; exists {
		return body, nil
	}
	return session.Continuation{}, session.ErrContinuationNotFound
}

func (s *brokenStore) LoadContinuationHead(ctx context.Context, sessionID string) (*session.Continuation, error) {
	if s.fault != "two_writes" {
		return s.ContinuationStore.LoadContinuationHead(ctx, sessionID)
	}
	if head, exists := s.heads[sessionID]; exists {
		return &head, nil
	}
	return nil, nil
}

func newBrokenStore(store session.ContinuationStore, fault string) *brokenStore {
	return &brokenStore{ContinuationStore: store, fault: fault, bodies: map[string]session.Continuation{}, heads: map[string]session.Continuation{}}
}
