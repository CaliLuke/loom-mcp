package agenttest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type captureState struct {
	hits int
}

type gate struct {
	inner   model.Client
	harness *Harness
}

var (
	// ErrCaptureSentinel stops a capture at the first recorded model request.
	ErrCaptureSentinel = errors.New("agenttest: capture first request sentinel")
	// ErrCaptureNoModelCall means the planner made no recorded model call.
	ErrCaptureNoModelCall = errors.New("agenttest: capture made no model call")
	// ErrCaptureHandled means the planner did not propagate the capture sentinel.
	ErrCaptureHandled = errors.New("agenttest: the planner handled the capture sentinel instead of returning it")
)

// CaptureFirstRequest runs a fresh session until the first model request is
// recorded, then fails that model invocation without consuming its client.
// Planners must return the sentinel error. Tools scheduled before that invocation
// follow the normal strict stub rules; model interceptors may short-circuit it.
func (h *Harness) CaptureFirstRequest(ctx context.Context, id agent.Ident, input Input, opts ...RunOption) (Request, error) {
	sessionID, err := h.newSession(ctx)
	if err != nil {
		return Request{}, err
	}
	state := &captureState{}
	result, err := h.runCapture(ctx, id, sessionID, input, state, opts...)
	if err != nil {
		return Request{}, err
	}
	calls := result.Record.Runs[0].Calls
	if len(calls) == 0 {
		return Request{}, ErrCaptureNoModelCall
	}
	h.mu.Lock()
	hits := state.hits
	h.mu.Unlock()
	if result.RunErr == nil || hits != 1 || len(calls) != 1 {
		return Request{}, fmt.Errorf("%w (gate hits=%d, recorded calls=%d, run failed=%t)", ErrCaptureHandled, hits, len(calls), result.RunErr != nil)
	}
	call := calls[0]
	if call.Completion == nil || call.Completion.Error == nil || !strings.Contains(call.Completion.Error.Message, ErrCaptureSentinel.Error()) {
		return Request{}, fmt.Errorf("%w: sentinel completion is missing", ErrCaptureHandled)
	}
	r, err := NewRequest(call.Started.ModelID, call.Request)
	if err != nil {
		return Request{}, err
	}
	key := call.Record.Key
	r.source = &Source{RunID: key.RunID, Seq: key.Seq, Attempt: key.Attempt}
	return r, nil
}

func (g gate) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if g.capture(ctx) {
		return nil, ErrCaptureSentinel
	}
	return g.inner.Complete(ctx, req)
}

func (g gate) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	if g.capture(ctx) {
		return nil, ErrCaptureSentinel
	}
	return g.inner.Stream(ctx, req)
}

// installGates retains direct clients and preserves their optional capabilities.
func (h *Harness) installGates() error {
	for _, id := range h.rt.ModelIDs() {
		inner, ok := h.rt.ModelClient(id)
		if !ok {
			return fmt.Errorf("agenttest: registered model %q disappeared", id)
		}
		h.models[id] = inner
		counter, hasCounter := inner.(model.TokenCounter)
		if !hasCounter {
			counter = nil
		}
		info, hasInfo := inner.(model.ContextInfo)
		if !hasInfo {
			info = nil
		}
		wrapped := model.WithCapabilities(gate{inner: inner, harness: h}, counter, info)
		if err := h.rt.RegisterModel(id, wrapped); err != nil {
			return fmt.Errorf("agenttest: gate model %q: %w", id, err)
		}
	}
	return nil
}

func (g gate) capture(ctx context.Context) bool {
	info, ok := model.CallInfoFrom(ctx)
	if !ok {
		return false
	}
	g.harness.mu.Lock()
	defer g.harness.mu.Unlock()
	state, ok := g.harness.capturing[info.RootRunID]
	if ok {
		state.hits++
	}
	return ok
}
