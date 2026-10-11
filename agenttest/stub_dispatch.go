package agenttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

type unusedResponseError struct {
	tool                string
	response, remaining int
}

func (e *unusedResponseError) Error() string {
	return fmt.Sprintf("agenttest: tool %q response %d: %d unused calls", e.tool, e.response, e.remaining)
}

// waitStubDelay does not invoke the response callback after observed cancellation.
func waitStubDelay(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// beforeStub reserves the response and recording slot atomically, then executes
// callbacks outside the table lock so concurrent tools cannot block each other.
func (h *Harness) beforeStub(ctx context.Context, input *runtime.BeforeToolInput) (*runtime.BeforeToolDecision, error) {
	call := input.Call
	h.stubs.mu.Lock()
	h.stubs.counts[call.Name]++
	slot := len(h.stubs.calls)
	h.stubs.calls = append(h.stubs.calls, RecordedCall{Tool: call.Name, Call: h.stubs.counts[call.Name], RunID: call.RunID, ToolCallID: call.ToolCallID, ParentToolCallID: call.ParentToolCallID, Raw: slices.Clone(call.Payload)})
	entry := h.stubs.entries[call.Name]
	if entry == nil && (h.stubs.passthroughAll || h.stubs.passthrough[call.Name]) {
		h.stubs.calls[slot].Passthrough = true
		h.stubs.mu.Unlock()
		return nil, nil
	}
	var execute func(context.Context) (*runtime.BeforeToolDecision, error)
	err := errors.New("unstubbed tool")
	if entry != nil {
		execute, err = entry.prepare(call)
	}
	if err != nil {
		failure := &StubError{Tool: call.Name, Call: h.stubs.counts[call.Name], RunID: call.RunID, ToolCallID: call.ToolCallID, Reason: err.Error(), Args: slices.Clone(call.Payload), cause: err}
		h.stubs.failures = append(h.stubs.failures, failure)
		h.stubs.calls[slot].Err = failure
		if entry != nil {
			entry.recordFailure(failure)
		}
		h.stubs.mu.Unlock()
		return nil, failure
	}
	h.stubs.mu.Unlock()
	return execute(ctx)
}

// prepare decodes and claims a response under the shared table lock.
func (s *ToolStub[P, R]) prepare(call planner.ToolRequest) (func(context.Context) (*runtime.BeforeToolDecision, error), error) {
	slot := len(s.calls)
	global := len(s.h.stubs.calls) - 1
	payload, err := s.tool.Payload.FromJSON(call.Payload)
	s.calls = append(s.calls, StubCall[P, R]{RunID: call.RunID, ToolCallID: call.ToolCallID, ParentToolCallID: call.ParentToolCallID, Payload: payload, Raw: slices.Clone(call.Payload)})
	s.h.stubs.calls[global].Payload = payload
	if err != nil {
		return nil, fmt.Errorf("payload decode failure: %w", err)
	}
	index, response, err := s.selectResponse(payload)
	if err != nil {
		return nil, err
	}
	s.calls[slot].Response = index
	s.h.stubs.calls[global].Response = index
	resultValue := s.resultValue
	return func(ctx context.Context) (*runtime.BeforeToolDecision, error) {
		if err := waitStubDelay(ctx, response.delay); err != nil {
			var zero R
			s.complete(slot, global, zero, err)
			return nil, err
		}
		result, handleErr := response.handle(ctx, payload)
		toolResult := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}
		if handleErr != nil {
			var zero R
			result = zero
			toolResult.Error = planner.ToolErrorFromError(handleErr)
		} else {
			value, err := resultValue(result)
			if err != nil {
				failure := s.resultDecodeFailure(global, err)
				s.complete(slot, global, result, failure)
				return nil, failure
			}
			toolResult.Result = value
			toolResult.Bounds = response.bounds
		}
		s.complete(slot, global, result, handleErr)
		return &runtime.BeforeToolDecision{Result: runtime.Executed(toolResult)}, nil
	}, nil
}

// resultDecodeFailure records a raw response rejected by the registered decoder.
// The reserved call slot preserves its arrival index across concurrent handlers.
func (s *ToolStub[P, R]) resultDecodeFailure(global int, err error) *StubError {
	s.h.stubs.mu.Lock()
	defer s.h.stubs.mu.Unlock()
	call := s.h.stubs.calls[global]
	failure := &StubError{Tool: call.Tool, Call: call.Call, RunID: call.RunID, ToolCallID: call.ToolCallID, Reason: "result decode failure: " + err.Error(), Args: slices.Clone(call.Raw), cause: err}
	s.h.stubs.failures = append(s.h.stubs.failures, failure)
	return failure
}

func (s *ToolStub[P, R]) complete(slot, global int, result R, err error) {
	s.h.stubs.mu.Lock()
	defer s.h.stubs.mu.Unlock()
	s.calls[slot].Result, s.calls[slot].Err = result, err
	s.h.stubs.calls[global].Result, s.h.stubs.calls[global].Err = result, err
}

func (s *ToolStub[P, R]) recordFailure(err error) {
	s.calls[len(s.calls)-1].Err = err
}

func (s *ToolStub[P, R]) diagnostics() []error {
	var diagnostics []error
	for i, response := range s.responses {
		if s.bounded && response.returns && response.bounds == nil {
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: tool %q response %d requires Bounds", s.tool.Name, i+1))
		}
		if !response.always && response.remaining > 0 {
			diagnostics = append(diagnostics, &unusedResponseError{tool: string(s.tool.Name), response: i + 1, remaining: response.remaining})
		}
	}
	return diagnostics
}
