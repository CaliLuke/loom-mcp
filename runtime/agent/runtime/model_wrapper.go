package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// This file implements a per-turn model.Client decorator that emits runtime
// planner events for accepted model output. The wrapper:
//   - Streams: publishes provisional text and thinking live, stages their
//     canonical commitment until finalization succeeds, and forwards safe usage
//     deltas as they arrive.
//   - Unary: emits assistant text/thinking from the final response and
//     reports usage when available.
//
// Critical invariants:
//   - Final tool calls are NOT emitted here; those are already surfaced to
//     planners via model.ChunkTypeToolCall and handled by the workflow loop.
//   - Runtime model tool-call argument deltas remain private until the complete
//     validated tool call is available. Legacy non-runtime event sinks retain
//     their existing best-effort delta behavior.
//   - Emission occurs in the planner activity context to keep ledger writes
//     deterministic and scoped to the current turn.

// eventDecoratedClient wraps a model.Client and forwards stream/unary content to
// PlannerEvents so the runtime ledger captures thinking/text/usage automatically.
type eventDecoratedClient struct {
	inner  model.Client
	events planner.PlannerEvents
}

// newEventDecoratedClient returns a client wrapper that emits PlannerEvents for
// assistant text, thinking blocks, and usage. When inner or events is nil, the
// inner client is returned unchanged.
func newEventDecoratedClient(inner model.Client, events planner.PlannerEvents) model.Client {
	if inner == nil || events == nil {
		return inner
	}
	return &eventDecoratedClient{
		inner:  inner,
		events: events,
	}
}

// Complete delegates to the inner client, then emits usage and assistant
// content (text + thinking) for the final response. If the adapter did not
// stamp model identity, the wrapper fills it from the request.
func (c *eventDecoratedClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	resp, err := c.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	canonicalizeModelToolUnavailableCalls(resp, req)
	if (resp.Usage != model.TokenUsage{}) {
		stampModelIdentity(&resp.Usage, req)
		c.events.UsageDelta(ctx, resp.Usage)
	}
	for i := range resp.Content {
		msg := resp.Content[i]
		if msg.Role != model.ConversationRoleAssistant {
			continue
		}
		emitMessageContent(ctx, c.events, &msg)
	}
	return resp, nil
}

func canonicalizeModelToolUnavailableCalls(response *model.Response, request *model.Request) {
	if response == nil {
		return
	}
	for index := range response.ToolCalls {
		if response.ToolCalls[index].Name == tools.ToolUnavailable {
			response.ToolCalls[index].Payload = exactToolUnavailablePayload(request)
		}
	}
}

// Stream delegates to the inner client and returns a Streamer that owns planner
// events. It emits usage and provisional presentation while receiving, then
// stages the canonical assistant message after finalization succeeds. The
// planner activity commits it only after the planner returns successfully. The
// request supplies model identity when usage chunks do not include one.
func (c *eventDecoratedClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	st, err := c.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	wrapped := &eventStream{
		inner:  st,
		events: c.events,
		ctx:    ctx,
		req:    req,
	}
	if presentation, ok := c.events.(planner.ModelPresentationEvents); ok {
		presentationID := presentation.StartModelPresentation(ctx)
		wrapped.presentation = presentation
		wrapped.presentationID = presentationID
	}
	return wrapped, nil
}

// eventStream decorates a model.Streamer to emit PlannerEvents for chunks. It
// carries the original request so usage chunks can be attributed and direct
// runtime tool calls can be canonicalized from the exact effective request.
type eventStream struct {
	inner  model.ValidatedStreamer
	events planner.PlannerEvents
	ctx    context.Context
	req    *model.Request
	parts  []model.Part

	presentation   planner.ModelPresentationEvents
	presentationID string
	finalizeMu     sync.Mutex
	finalized      bool
	finalizeErr    error
}

// EmitsPlannerEvents reports that the stream owns planner presentation and usage
// events. Recv publishes safe incremental events, and Finalize stages validated
// presentation content for the planner activity. Helpers such as
// planner.ConsumeStream use this marker to avoid publishing each chunk twice.
func (*eventStream) EmitsPlannerEvents() bool {
	return true
}

// Recv forwards safe incremental events and stages model-authored presentation
// content until terminal validation accepts the complete stream.
//
// Contract:
//   - Final tool calls are passed through untouched for the planner/workflow to
//     handle.
//   - Runtime model tool-call argument deltas remain private. Legacy event sinks
//     may still receive them as best-effort UX signals. Internal tool argument
//     deltas are never emitted because their payload is replaced at the activity
//     boundary.
func (s *eventStream) Recv() (model.Chunk, error) {
	ch, err := s.inner.Recv()
	if err != nil {
		return ch, err
	}
	switch value := ch.(type) {
	case model.ToolCallDeltaChunk:
		if s.presentation == nil && value.Delta.Name != tools.ToolUnavailable {
			s.events.ToolCallArgsDelta(s.ctx, value.Delta.ID, value.Delta.Name, value.Delta.Delta)
		}
	case model.TextChunk:
		s.stageMessageContent(&value.Message)
	case model.ThinkingChunk:
		s.stageThinkingParts(&value.Message)
	case model.ToolCallChunk:
		if value.ToolCall.Name == tools.ToolUnavailable {
			value.ToolCall.Payload = exactToolUnavailablePayload(s.req)
			ch = value
		}
	case model.UsageChunk:
		stampModelIdentity(&value.Usage, s.req)
		s.events.UsageDelta(s.ctx, value.Usage)
		ch = value
	}
	return ch, nil
}

func (s *eventStream) stageMessageContent(message *model.Message) {
	s.stageThinkingParts(message)
	for _, part := range message.Parts {
		var text string
		switch content := part.(type) {
		case model.TextPart:
			text = content.Text
		case model.CitationsPart:
			text = content.Text
		}
		if text != "" {
			s.parts = append(s.parts, model.TextPart{Text: text})
			if s.presentation != nil {
				s.presentation.PublishModelText(s.ctx, s.presentationID, text)
			}
		}
	}
}

func (s *eventStream) stageThinkingParts(message *model.Message) {
	for _, part := range message.Parts {
		if thinking, ok := part.(model.ThinkingPart); ok {
			s.stageThinking(thinking)
		}
	}
}

func (s *eventStream) stageThinking(thinking model.ThinkingPart) {
	thinking.Redacted = append([]byte(nil), thinking.Redacted...)
	s.parts = append(s.parts, thinking)
	if s.presentation == nil {
		return
	}
	s.presentation.PublishModelThinking(s.ctx, s.presentationID, thinking)
}

func (s *eventStream) flushStagedEvents(response *model.Response) error {
	parts := s.parts
	s.parts = nil
	if s.presentation != nil {
		return s.presentation.CommitModelPresentation(s.ctx, s.presentationID, response)
	}
	for _, part := range parts {
		switch value := part.(type) {
		case model.ThinkingPart:
			s.events.PlannerThinkingBlock(s.ctx, value)
		case model.TextPart:
			s.events.AssistantChunk(s.ctx, value.Text)
		}
	}
	return nil
}

func (s *eventStream) Close() error {
	return s.inner.Close()
}

func (s *eventStream) Response() *model.Response {
	response := s.inner.Response()
	canonicalizeModelToolUnavailableCalls(response, s.req)
	return response
}

func (s *eventStream) Finalize(primaryErr error) error {
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	if s.finalized {
		return s.finalizeErr
	}
	s.finalized = true

	err := s.inner.Finalize(primaryErr)
	if err != nil {
		s.parts = nil
		if s.presentation != nil {
			s.presentation.FinishModelPresentation(s.ctx, s.presentationID, false)
		}
		s.finalizeErr = err
		return s.finalizeErr
	}
	response := s.Response()
	if response == nil {
		s.finalizeErr = errors.New("validated model stream finalized without a canonical response")
		if s.presentation != nil {
			s.presentation.FinishModelPresentation(s.ctx, s.presentationID, false)
		}
		return s.finalizeErr
	}
	commitErr := s.flushStagedEvents(response)
	if s.presentation != nil {
		s.presentation.FinishModelPresentation(s.ctx, s.presentationID, commitErr == nil)
	}
	s.finalizeErr = commitErr
	return s.finalizeErr
}

// cacheConfiguredClient wraps a model.Client and applies the agent CachePolicy
// to each request. It sets Request.Cache only when it is currently nil so
// explicit per-request CacheOptions take precedence over the agent defaults.
type cacheConfiguredClient struct {
	inner model.Client
	cache CachePolicy
}

func newCacheConfiguredClient(inner model.Client, cache CachePolicy) model.Client {
	if inner == nil {
		return nil
	}
	if !cache.AfterSystem && !cache.AfterTools {
		return inner
	}
	return &cacheConfiguredClient{
		inner: inner,
		cache: cache,
	}
}

func (c *cacheConfiguredClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	applyCachePolicy(req, c.cache)
	return c.inner.Complete(ctx, req)
}

func (c *cacheConfiguredClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	applyCachePolicy(req, c.cache)
	return c.inner.Stream(ctx, req)
}

// toolUnavailableConfiguredClient ensures the runtime-owned tool_unavailable tool
// is always present in tool-aware requests. Some providers require that any tool
// referenced in tool_use history appears in the current request tool list.
type toolUnavailableConfiguredClient struct {
	inner model.Client
}

type toolPolicyEnvelope struct {
	Active  bool
	Allowed []tools.Ident
}

type toolPolicyConfiguredClient struct {
	inner  model.Client
	policy toolPolicyEnvelope
}

func newToolUnavailableConfiguredClient(inner model.Client) model.Client {
	if inner == nil {
		return nil
	}
	return &toolUnavailableConfiguredClient{inner: inner}
}

func newToolPolicyConfiguredClient(inner model.Client, policy toolPolicyEnvelope) model.Client {
	if inner == nil || !policy.Active {
		return inner
	}
	return &toolPolicyConfiguredClient{
		inner:  inner,
		policy: cloneToolPolicyEnvelope(policy),
	}
}

func (c *toolUnavailableConfiguredClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	ensureToolUnavailableDefinition(req)
	return c.inner.Complete(ctx, req)
}

func (c *toolUnavailableConfiguredClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	ensureToolUnavailableDefinition(req)
	return c.inner.Stream(ctx, req)
}

func (c *toolPolicyConfiguredClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	applyModelToolPolicy(req, c.policy)
	return c.inner.Complete(ctx, req)
}

func (c *toolPolicyConfiguredClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	applyModelToolPolicy(req, c.policy)
	return c.inner.Stream(ctx, req)
}

// applyCachePolicy populates Request.Cache from the agent CachePolicy when no
// explicit CacheOptions are present on the request.
func applyCachePolicy(req *model.Request, cache CachePolicy) {
	if req == nil || req.Cache != nil {
		return
	}
	if !cache.AfterSystem && !cache.AfterTools {
		return
	}
	req.Cache = &model.CacheOptions{
		AfterSystem: cache.AfterSystem,
		AfterTools:  cache.AfterTools,
	}
}

func ensureToolUnavailableDefinition(req *model.Request) {
	if req == nil {
		return
	}
	if !requestMayReferenceTools(req) {
		return
	}
	name := tools.ToolUnavailable.String()
	for _, def := range req.Tools {
		if def != nil && def.Name == name {
			return
		}
	}
	req.Tools = append(req.Tools, toolUnavailableToolDefinition())
}

func requestMayReferenceTools(req *model.Request) bool {
	if req == nil {
		return false
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return true
	}
	for _, msg := range req.Messages {
		if msg == nil {
			continue
		}
		for _, part := range msg.Parts {
			switch part.(type) {
			case model.ToolUsePart, model.ToolResultPart:
				return true
			}
		}
	}
	return false
}

func applyModelToolPolicy(req *model.Request, policy toolPolicyEnvelope) {
	if req == nil || !policy.Active {
		return
	}
	allowed := make(map[string]struct{}, len(policy.Allowed))
	for _, tool := range policy.Allowed {
		allowed[tool.String()] = struct{}{}
	}
	// tool_unavailable is part of every non-empty tool-aware request so strict
	// providers can replay its tool-use history. An empty active allowlist is an
	// explicit tool-free turn and must not re-admit the internal definition.
	if len(policy.Allowed) > 0 {
		allowed[tools.ToolUnavailable.String()] = struct{}{}
	}
	if len(req.Tools) > 0 {
		filtered := make([]*model.ToolDefinition, 0, len(req.Tools))
		for _, def := range req.Tools {
			if def == nil {
				continue
			}
			if _, ok := allowed[def.Name]; ok {
				filtered = append(filtered, def)
			}
		}
		req.Tools = filtered
	}
	if req.ToolChoice == nil {
		return
	}
	switch req.ToolChoice.Mode {
	case model.ToolChoiceModeTool:
		if _, ok := allowed[req.ToolChoice.Name]; !ok {
			req.ToolChoice = nil
		}
	case model.ToolChoiceModeAny:
		if len(req.Tools) == 0 {
			req.ToolChoice = nil
		}
	case model.ToolChoiceModeAuto, model.ToolChoiceModeNone:
	}
}

func cloneToolPolicyEnvelope(in toolPolicyEnvelope) toolPolicyEnvelope {
	return toolPolicyEnvelope{
		Active:  in.Active,
		Allowed: cloneToolIdents(in.Allowed),
	}
}

// emitMessageContent forwards assistant text and thinking parts from a message.
func emitMessageContent(ctx context.Context, ev planner.PlannerEvents, msg *model.Message) {
	if ev == nil || msg == nil || len(msg.Parts) == 0 {
		return
	}
	// Emit thinking parts first to preserve natural ordering semantics.
	emitThinkingParts(ctx, ev, msg)
	for _, p := range msg.Parts {
		if tp, ok := p.(model.TextPart); ok && tp.Text != "" {
			ev.AssistantChunk(ctx, tp.Text)
		}
	}
}

// stampModelIdentity fills Model and ModelClass on usage when the adapter left
// them empty. This ensures attribution is always present by the time usage
// reaches the hook bus, using the request as the fallback source.
func stampModelIdentity(usage *model.TokenUsage, req *model.Request) {
	if usage.Model == "" && req.Model != "" {
		usage.Model = req.Model
	}
	if usage.ModelClass == "" && req.ModelClass != "" {
		usage.ModelClass = req.ModelClass
	}
}

// emitThinkingParts forwards structured thinking blocks from a message.
func emitThinkingParts(ctx context.Context, ev planner.PlannerEvents, msg *model.Message) {
	if ev == nil || msg == nil || len(msg.Parts) == 0 {
		return
	}
	for _, p := range msg.Parts {
		if tp, ok := p.(model.ThinkingPart); ok {
			ev.PlannerThinkingBlock(ctx, tp)
		}
	}
}
