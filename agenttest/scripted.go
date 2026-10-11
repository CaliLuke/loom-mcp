// Package agenttest provides deterministic model scripts for agent tests.
package agenttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Script implements model.Provider with deterministic, inspectable turns.
// Use Client to enforce real model request and output contracts.
type Script struct {
	mu       sync.Mutex
	turns    []scriptTurn
	requests []*model.Request
	errors   []error
	client   model.Client
	anyOrder bool
	limits   model.ContextLimits
}

type scriptTurn struct {
	turn     Turn
	response scriptResponse
	consumed bool
}

// ScriptError reports a matcher failure or exhaustion with request context.
// CallIndex and TurnIndex are one-based; TurnIndex is zero on exhaustion.
type ScriptError struct {
	CallIndex int
	TurnIndex int
	Summary   string
	Cause     error
}

// ErrScriptExhausted means a request arrived after all turns were consumed.
var ErrScriptExhausted = errors.New("agenttest: script exhausted")

// NewScript builds a script, rejecting invalid builders and duplicate call IDs.
func NewScript(turns ...Turn) (*Script, error) {
	script := &Script{turns: make([]scriptTurn, len(turns))}
	ids := make(map[string]struct{})
	for index, turn := range turns {
		response, err := buildScriptResponse(turn, index+1, ids)
		if err != nil {
			return nil, fmt.Errorf("agenttest: turn %d: %w", index+1, err)
		}
		turn.matches = slices.Clone(turn.matches)
		script.turns[index] = scriptTurn{turn: turn, response: response}
	}
	client, err := model.NewClient(script)
	if err != nil {
		return nil, err
	}
	script.client = client
	return script, nil
}

// ScriptClient builds a validated client and reports script diagnostics at cleanup.
func ScriptClient(t testing.TB, turns ...Turn) (model.Client, *Script) {
	t.Helper()
	script, err := NewScript(turns...)
	if err != nil {
		t.Fatalf("build model script: %v", err)
		return nil, nil
	}
	t.Cleanup(func() {
		if err := script.Err(); err != nil {
			t.Error(err)
		}
	})
	return script.Client(), script
}

// Client returns the single validated client built by NewScript.
func (s *Script) Client() model.Client {
	return s.client
}

// Complete consumes a matching turn and returns its raw provider response.
func (s *Script) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	turn, err := s.takeTurn(ctx, request)
	if err != nil {
		return nil, err
	}
	if turn.turn.failure != nil {
		return nil, turn.turn.failure
	}
	return turn.response.complete(request, turn.turn.fragments), nil
}

// Stream consumes a matching turn and returns a raw scripted stream.
func (s *Script) Stream(ctx context.Context, request *model.Request) (model.Streamer, error) {
	turn, err := s.takeTurn(ctx, request)
	if err != nil {
		return nil, err
	}
	if turn.turn.failure != nil {
		return nil, turn.turn.failure
	}
	return &scriptStream{ctx: ctx, chunks: turn.response.chunks(request, turn.turn.fragments), failure: turn.turn.streamFailure, failAfter: turn.turn.streamAfter}, nil
}

// AnyOrder selects the first unconsumed turn whose matchers all pass.
func (s *Script) AnyOrder() *Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.anyOrder = true
	return s
}

// WithContextLimits sets the limits exposed by Client's ContextInfo capability.
func (s *Script) WithContextLimits(limits model.ContextLimits) *Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = limits
	return s
}

// ContextLimits reports configured limits and the request's model identity.
func (s *Script) ContextLimits(_ context.Context, request *model.Request) (model.ContextLimits, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits, request.Model, s.limits.ContextWindow > 0
}

// Requests returns a detached slice of request pointers as received by the provider.
func (s *Script) Requests() []*model.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Remaining reports the number of unconsumed turns.
func (s *Script) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, turn := range s.turns {
		if !turn.consumed {
			count++
		}
	}
	return count
}

// Err joins all script diagnostics and lists every unconsumed turn.
// Intentional Fail turns and output validation failures are not script diagnostics.
func (s *Script) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	diagnostics := slices.Clone(s.errors)
	for index, turn := range s.turns {
		if !turn.consumed {
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: unconsumed turn %d", index+1))
		}
	}
	return errors.Join(diagnostics...)
}

// Error renders the failing call, turn, matcher, and bounded request summary.
func (e *ScriptError) Error() string {
	return fmt.Sprintf("agenttest: call %d, turn %d: %v (%s)", e.CallIndex, e.TurnIndex, e.Cause, e.Summary)
}

// Unwrap exposes the matcher error or ErrScriptExhausted.
func (e *ScriptError) Unwrap() error {
	return e.Cause
}

func buildScriptResponse(turn Turn, index int, ids map[string]struct{}) (scriptResponse, error) {
	var response scriptResponse
	for _, part := range turn.parts {
		if part.apply == nil {
			return response, errors.New("invalid response part")
		}
		if err := part.apply(&response); err != nil {
			return response, err
		}
	}
	for callIndex := range response.calls {
		call := &response.calls[callIndex]
		if call.ID == "" {
			call.ID = fmt.Sprintf("scripted-%d-%d", index, callIndex+1)
		}
		if _, exists := ids[call.ID]; exists {
			return response, fmt.Errorf("duplicate call ID %q", call.ID)
		}
		ids[call.ID] = struct{}{}
	}
	if response.stop == "" {
		response.stop = "end_turn"
		if len(response.calls) > 0 {
			response.stop = "tool_use"
		}
	}
	chunks := response.chunks(&model.Request{}, turn.fragments)
	if turn.streamFailure != nil && turn.streamAfter > len(chunks) {
		return response, fmt.Errorf("FailStreamAfter(%d) exceeds the turn's %d chunks", turn.streamAfter, len(chunks))
	}
	// Reject malformed chunk shapes before either client path can observe them.
	// Schema validation remains request-owned at the real model.Client boundary.
	var builder model.StreamResponseBuilder
	for _, chunk := range chunks {
		if err := builder.Add(chunk); err != nil {
			return response, fmt.Errorf("invalid turn shape: %w", err)
		}
	}
	return response, nil
}

// takeTurn records each request and atomically selects/consumes its turn.
func (s *Script) takeTurn(ctx context.Context, request *model.Request) (scriptTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	if err := ctx.Err(); err != nil {
		return scriptTurn{}, err
	}
	var mismatch error
	first := -1
	for index := range s.turns {
		turn := &s.turns[index]
		if turn.consumed {
			continue
		}
		err := matchRequest(turn.turn.matches, request)
		if err == nil {
			turn.consumed = true
			return *turn, nil
		}
		if first == -1 {
			first = index
		}
		mismatch = errors.Join(mismatch, err)
		if !s.anyOrder {
			break
		}
	}
	turnIndex := 0
	if first == -1 {
		mismatch = ErrScriptExhausted
	} else {
		turnIndex = first + 1
	}
	diagnostic := &ScriptError{CallIndex: len(s.requests), TurnIndex: turnIndex, Summary: requestSummary(request), Cause: mismatch}
	s.errors = append(s.errors, diagnostic)
	return scriptTurn{}, diagnostic
}
