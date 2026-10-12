package agenttest

import (
	"context"
	"slices"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// Session runs dynamically constructed inputs in one shared runtime session.
// Run calls are serialized. Callers supply conversation history explicitly;
// registered runtime continuation and memory policies retain their normal effect.
type Session struct {
	mu      sync.Mutex
	harness *Harness
	result  SessionResult
}

// OpenSession creates an empty session without submitting a workflow.
func (h *Harness) OpenSession(ctx context.Context) (*Session, error) {
	id, err := h.newSession(ctx)
	if err != nil {
		return nil, err
	}
	return &Session{harness: h, result: SessionResult{SessionID: id, Runs: []*Result{}, Trajectory: trajectory.Session{}}}, nil
}

// Run executes one turn, retaining any returned evidence even on harness errors.
// Agent RunErr is an outcome and does not close the session.
func (s *Session) Run(ctx context.Context, id agent.Ident, input Input, opts ...RunOption) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.harness.run(ctx, id, s.result.SessionID, input, opts...)
	if result != nil {
		s.result.Runs = append(s.result.Runs, result)
		s.result.Trajectory = append(s.result.Trajectory, result.Trajectory)
	}
	return result, err
}

// Result returns an owned snapshot of the completed run lists. The individual
// results are the same immutable evidence objects returned by Run.
func (s *Session) Result() *SessionResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &SessionResult{SessionID: s.result.SessionID, Runs: slices.Clone(s.result.Runs), Trajectory: slices.Clone(s.result.Trajectory)}
}
