package agenttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ToolStub supplies ordered responses for one registered activity tool.
type ToolStub[P, R any] struct {
	h           *Harness
	tool        tools.TypedTool[P, R]
	bounded     bool
	resultValue func(R) (any, error)
	responses   []*Response[P, R]
	calls       []StubCall[P, R]
}

// Response is one declared stub outcome. Configure responses before calling Run.
type Response[P, R any] struct {
	stub      *ToolStub[P, R]
	handle    func(context.Context, P) (R, error)
	remaining int
	returns   bool
	always    bool
	matches   []ArgMatch[P]
	bounds    *agent.Bounds
	delay     time.Duration
}

// StubCall records one stub invocation. Response is one-based, or zero on failure.
type StubCall[P, R any] struct {
	RunID, ToolCallID, ParentToolCallID string
	Payload                             P
	Raw                                 rawjson.Message
	Response                            int
	Result                              R
	Err                                 error
}

// RecordedCall records every activity call reaching the harness, in arrival order.
// Call and Response are one-based; Response is zero when no stub response ran.
type RecordedCall struct {
	Tool                                tools.Ident
	Call                                int
	RunID, ToolCallID, ParentToolCallID string
	Payload                             any
	Raw                                 rawjson.Message
	Response                            int
	Result                              any
	Err                                 error
	Passthrough                         bool
}

// StubError records a harness dispatch failure. The runtime exposes it as a tool
// error; Run returns it as a harness error after the run finishes.
type StubError struct {
	Tool              tools.Ident
	Call              int
	RunID, ToolCallID string
	Reason            string
	Args              rawjson.Message
	cause             error
}

type stubTable struct {
	mu             sync.Mutex
	entries        map[tools.Ident]stubEntry
	order          []stubEntry
	counts         map[tools.Ident]int
	calls          []RecordedCall
	setup          []error
	failures       []*StubError
	passthrough    map[tools.Ident]bool
	passthroughAll bool
}

type stubEntry interface {
	prepare(planner.ToolRequest) (func(context.Context) (*runtime.BeforeToolDecision, error), error)
	diagnostics() []error
	recordFailure(error)
}

// ErrStubExhausted marks a call for which all declared responses were consumed.
var ErrStubExhausted = errors.New("stub exhausted")

// Stub declares one typed stub. The tool must be registered as an activity tool.
// Duplicate, unknown and non-activity declarations are reported to New's test
// immediately, or through Run and Close for NewHarness callers.
func Stub[P, R any](h *Harness, tool tools.TypedTool[P, R]) *ToolStub[P, R] {
	stub := &ToolStub[P, R]{h: h, tool: tool, resultValue: func(result R) (any, error) {
		return result, nil
	}}
	spec, ok := h.rt.ToolSpec(tool.Name)
	if !ok {
		h.stubSetupError(fmt.Errorf("agenttest: tool %q is not registered", tool.Name))
		return stub
	}
	mode, ok := h.rt.ToolDispatchMode(tool.Name)
	if !ok || mode != runtime.DispatchActivity {
		h.stubSetupError(fmt.Errorf("agenttest: tool %q: inline/agent-as-tool not stubbable; inline and agent-as-tool stubbing is a runtime follow-up", tool.Name))
		return stub
	}
	if tool.Payload.FromJSON == nil {
		h.stubSetupError(fmt.Errorf("agenttest: tool %q payload decoder is required", tool.Name))
		return stub
	}
	stub.bounded = spec.Bounds != nil
	h.stubs.mu.Lock()
	_, duplicate := h.stubs.entries[tool.Name]
	if !duplicate {
		h.stubs.entries[tool.Name] = stub
		h.stubs.order = append(h.stubs.order, stub)
	}
	h.stubs.mu.Unlock()
	if duplicate {
		h.stubSetupError(fmt.Errorf("agenttest: tool %q stubbed twice", tool.Name))
	}
	return stub
}

// Return appends a successful response, consumed once by default.
func (s *ToolStub[P, R]) Return(result R) *Response[P, R] {
	response := s.Handle(func(context.Context, P) (R, error) {
		return result, nil
	})
	s.h.stubs.mu.Lock()
	response.returns = true
	s.h.stubs.mu.Unlock()
	return response
}

// Fail appends a model-visible tool error. It does not fail the run itself.
func (s *ToolStub[P, R]) Fail(err error) *Response[P, R] {
	if err == nil {
		s.h.stubSetupError(fmt.Errorf("agenttest: tool %q Fail requires an error", s.tool.Name))
	}
	return s.Handle(func(context.Context, P) (R, error) {
		var zero R
		return zero, err
	})
}

// Handle appends a callback response. Callbacks run outside the table lock and
// must support concurrent calls. A non-nil error discards the callback's result.
func (s *ToolStub[P, R]) Handle(handle func(context.Context, P) (R, error)) *Response[P, R] {
	if handle == nil {
		s.h.stubSetupError(fmt.Errorf("agenttest: tool %q Handle requires a callback", s.tool.Name))
	}
	response := &Response[P, R]{stub: s, handle: handle, remaining: 1}
	s.h.stubs.mu.Lock()
	s.responses = append(s.responses, response)
	s.h.stubs.mu.Unlock()
	return response
}

// Calls returns a snapshot in arrival order. Typed values are shallow copies;
// raw bytes are copied. The caller owns synchronization for referenced values.
func (s *ToolStub[P, R]) Calls() []StubCall[P, R] {
	s.h.stubs.mu.Lock()
	defer s.h.stubs.mu.Unlock()
	calls := slices.Clone(s.calls)
	for i := range calls {
		calls[i].Raw = slices.Clone(calls[i].Raw)
	}
	return calls
}

// Error includes the tool, call index, correlation IDs and received arguments.
func (e *StubError) Error() string {
	return fmt.Sprintf("agenttest: tool %q call %d run %q tool_call %q: %s; args=%s", e.Tool, e.Call, e.RunID, e.ToolCallID, e.Reason, e.Args)
}

// Unwrap exposes ErrStubExhausted, payload decode errors and RawStub result
// decode errors to errors.Is/As.
func (e *StubError) Unwrap() error {
	return e.cause
}

// ToolCalls returns an arrival-ordered snapshot, including failures and passthrough.
// Payload and Result are shallow copies; Raw bytes are copied.
func (h *Harness) ToolCalls() []RecordedCall {
	h.stubs.mu.Lock()
	defer h.stubs.mu.Unlock()
	calls := slices.Clone(h.stubs.calls)
	for i := range calls {
		calls[i].Raw = slices.Clone(calls[i].Raw)
	}
	return calls
}

func (h *Harness) stubSetupError(err error) {
	h.stubs.mu.Lock()
	h.stubs.setup = append(h.stubs.setup, err)
	h.stubs.mu.Unlock()
	if h.tb != nil {
		h.tb.Helper()
		h.tb.Error(err)
	}
}

// stubDiagnostics validates finished response declarations and, on cleanup,
// joins every dispatch failure and unused finite response.
func (h *Harness) stubDiagnostics(cleanup bool) error {
	h.stubs.mu.Lock()
	defer h.stubs.mu.Unlock()
	diagnostics := slices.Clone(h.stubs.setup)
	if cleanup {
		for _, failure := range h.stubs.failures {
			diagnostics = append(diagnostics, failure)
		}
	}
	for _, entry := range h.stubs.order {
		for _, err := range entry.diagnostics() {
			var unused *unusedResponseError
			if cleanup || !errors.As(err, &unused) {
				diagnostics = append(diagnostics, err)
			}
		}
	}
	return errors.Join(diagnostics...)
}

// stubRunErrors selects failures by correlation ID, keeping concurrent runs isolated.
func (h *Harness) stubRunErrors(runID string) error {
	h.stubs.mu.Lock()
	defer h.stubs.mu.Unlock()
	var failures []error
	for _, failure := range h.stubs.failures {
		if failure.RunID == runID {
			failures = append(failures, failure)
		}
	}
	return errors.Join(failures...)
}
