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
	calls map[model.CallInfo]struct{}
}

type captureContextKey struct {
	harness *Harness
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
// If any registered agent has a non-nil History policy, capture returns a
// *CaptureHistoryError before creating a session or invoking any policy or model.
func (h *Harness) CaptureFirstRequest(ctx context.Context, id agent.Ident, input Input, opts ...RunOption) (Request, error) {
	if agents := h.rt.AgentsWithHistoryPolicy(); len(agents) > 0 {
		return Request{}, &CaptureHistoryError{AgentID: agents[0]}
	}
	sessionID, err := h.newSession(ctx)
	if err != nil {
		return Request{}, err
	}
	state := &captureState{calls: make(map[model.CallInfo]struct{})}
	result, err := h.runCapture(ctx, id, sessionID, input, state, opts...)
	if err != nil {
		return Request{}, err
	}
	calls := result.Record.Runs[0].Calls
	if len(calls) == 0 {
		for _, child := range result.Record.Runs[1:] {
			if len(child.Calls) > 0 {
				return Request{}, fmt.Errorf("agenttest: capture recorded a model call in child run %s; capture the child agent directly", child.RunID)
			}
		}
		return Request{}, ErrCaptureNoModelCall
	}
	h.mu.Lock()
	hits := len(state.calls)
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

// captureGate preserves optional capabilities using comparable pointer adapters,
// so exact gate identity checks also accept non-comparable provider values.
func captureGate(inner model.Client, h *Harness) model.Client {
	base := &gate{inner: inner, harness: h}
	counter, _ := inner.(model.TokenCounter)
	info, _ := inner.(model.ContextInfo)
	parts, _ := inner.(model.ToolResultPartsInfo)
	configuration, _ := inner.(model.ProviderConfigurationInfo)
	// Let WithCapabilitySet remove typed nil capabilities before wrapping them.
	capabilities := model.WithCapabilitySet(base, model.Capabilities{Counter: counter, Info: info, ToolResultParts: parts, ProviderConfiguration: configuration})
	counter, _ = capabilities.(model.TokenCounter)
	info, _ = capabilities.(model.ContextInfo)
	parts, _ = capabilities.(model.ToolResultPartsInfo)
	configuration, _ = capabilities.(model.ProviderConfigurationInfo)
	if counter != nil {
		counter = &struct{ model.TokenCounter }{counter}
	}
	if info != nil {
		info = &struct{ model.ContextInfo }{info}
	}
	if configuration != nil {
		configuration = &struct {
			model.ProviderConfigurationInfo
		}{configuration}
	}
	if parts != nil {
		parts = &struct{ model.ToolResultPartsInfo }{parts}
	}
	return model.WithCapabilitySet(base, model.Capabilities{Counter: counter, Info: info, ToolResultParts: parts, ProviderConfiguration: configuration})
}

// gateModel runs under the runtime registry lock, before clients are exposed.
func (h *Harness) gateModel(id string, inner model.Client) model.Client {
	wrapped := captureGate(inner, h)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.models[id] = inner
	h.gates[id] = wrapped
	return wrapped
}

// verifyGates must run after Seal succeeds, when registry identities are immutable.
func (h *Harness) verifyGates(expected map[string]model.Client) error {
	for _, id := range h.rt.ModelIDs() {
		client, ok := h.rt.ModelClient(id)
		if !ok || client != expected[id] {
			return fmt.Errorf("agenttest: model %q capture gate changed during registration", id)
		}
	}
	return nil
}

// capture uses the harness run context for retained clients without CallInfo.
// Interceptor side calls share their outer call identity and count only once.
func (g gate) capture(ctx context.Context) bool {
	info, hasInfo := model.CallInfoFrom(ctx)
	g.harness.mu.Lock()
	defer g.harness.mu.Unlock()
	state, _ := ctx.Value(captureContextKey{harness: g.harness}).(*captureState)
	if state == nil && hasInfo {
		state = g.harness.capturing[info.RootRunID]
	}
	if state == nil {
		return false
	}
	if hasInfo {
		state.calls[info] = struct{}{}
	}
	return true
}
