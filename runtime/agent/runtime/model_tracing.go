package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type (
	modelTraceConfig struct {
		ModelID              string
		AgentID              string
		AgentName            string
		ConversationID       string
		CaptureGenAIMessages bool
	}

	tracedClient struct {
		inner  model.Client
		tracer telemetry.Tracer
		logger telemetry.Logger
		config modelTraceConfig
	}

	tracedStream struct {
		inner model.ValidatedStreamer
		span  telemetry.Span
		ctx   context.Context

		mu    sync.Mutex
		usage model.TokenUsage

		output *genAIStreamAccumulator

		startedAt          time.Time
		firstChunkRecorded bool
		endOnce            sync.Once
	}
)

func newTracedClient(inner model.Client, tracer telemetry.Tracer, logger telemetry.Logger, config modelTraceConfig) model.Client {
	if inner == nil {
		return nil
	}
	if tracer == nil {
		tracer = telemetry.NewNoopTracer()
	}
	if logger == nil {
		logger = telemetry.NewNoopLogger()
	}
	return &tracedClient{
		inner:  inner,
		tracer: tracer,
		logger: logger,
		config: config,
	}
}

func (c *tracedClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	ctx, span := c.tracer.Start(
		ctx,
		"model.complete",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(modelSpanAttrs(c.config, req)...),
	)
	defer span.End()

	resp, err := c.inner.Complete(ctx, req)
	span.SetAttributes(modelSpanAttrs(c.config, req)...)
	c.recordInputMessages(span, req)
	if err != nil {
		if !telemetry.ShouldRecordSpanError(ctx, err) {
			span.SetStatus(codes.Unset, "")
			return resp, err
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "model complete failed")
		c.logger.Error(
			ctx,
			"model complete failed",
			"model_id", c.config.ModelID,
			"err", err,
		)
		return resp, err
	}
	if (resp.Usage != model.TokenUsage{}) {
		span.SetAttributes(modelUsageAttrs(resp.Usage)...)
		span.AddEvent(
			"model.usage",
			"input_tokens", resp.Usage.InputTokens,
			"output_tokens", resp.Usage.OutputTokens,
			"total_tokens", resp.Usage.TotalTokens,
			"cache_read_tokens", resp.Usage.CacheReadTokens,
			"cache_write_tokens", resp.Usage.CacheWriteTokens,
		)
	}
	if resp.StopReason != "" {
		span.SetAttributes(telemetry.AttrGenAIResponseFinishReasons.StringSlice([]string{resp.StopReason}))
		span.AddEvent("model.stop", "reason", resp.StopReason)
	}
	c.recordOutputMessages(span, responseOutputMessages(resp, req), resp.StopReason)
	span.SetStatus(codes.Ok, "ok")
	return resp, nil
}

func (c *tracedClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	startedAt := time.Now()
	ctx, span := c.tracer.Start(
		ctx,
		"model.stream",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(modelSpanAttrs(c.config, req)...),
	)

	st, err := c.inner.Stream(ctx, req)
	span.SetAttributes(modelSpanAttrs(c.config, req)...)
	c.recordInputMessages(span, req)
	if err != nil {
		if !telemetry.ShouldRecordSpanError(ctx, err) {
			span.SetStatus(codes.Unset, "")
			span.End()
			return nil, err
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "model stream failed")
		span.End()
		c.logger.Error(
			ctx,
			"model stream failed",
			"model_id", c.config.ModelID,
			"err", err,
		)
		return nil, err
	}
	ts := &tracedStream{
		inner:     st,
		span:      span,
		ctx:       ctx,
		startedAt: startedAt,
	}
	if c.config.CaptureGenAIMessages {
		ts.output = newGenAIStreamAccumulator(req)
	}
	return ts, nil
}

func (s *tracedStream) Recv() (model.Chunk, error) {
	ch, err := s.inner.Recv()
	if err != nil {
		return ch, err
	}
	if usageChunk, ok := ch.(model.UsageChunk); ok {
		usage := usageChunk.Usage
		s.mu.Lock()
		if s.usage.Model == "" {
			s.usage.Model = usage.Model
		}
		if s.usage.ModelClass == "" {
			s.usage.ModelClass = usage.ModelClass
		}
		s.usage.InputTokens += usage.InputTokens
		s.usage.OutputTokens += usage.OutputTokens
		s.usage.TotalTokens += usage.TotalTokens
		s.usage.CacheReadTokens += usage.CacheReadTokens
		s.usage.CacheWriteTokens += usage.CacheWriteTokens
		s.mu.Unlock()
	}
	if isFirstGenAIOutputChunk(ch.Kind()) {
		s.recordFirstChunk()
	}
	s.recordOutputChunk(ch)
	if stop, ok := ch.(model.StopChunk); ok && stop.Reason != "" {
		s.span.SetAttributes(telemetry.AttrGenAIResponseFinishReasons.StringSlice([]string{stop.Reason}))
		s.span.AddEvent("model.stop", "reason", stop.Reason)
	}
	return ch, nil
}

// EmitsPlannerEvents forwards the inner stream's event-ownership marker so
// planner helpers do not publish accepted chunks twice through tracing.
func (s *tracedStream) EmitsPlannerEvents() bool {
	owned, ok := s.inner.(interface{ EmitsPlannerEvents() bool })
	return ok && owned.EmitsPlannerEvents()
}

func (s *tracedStream) Close() error {
	return s.inner.Close()
}

func (s *tracedStream) Metadata() map[string]any {
	return s.inner.Metadata()
}

func (s *tracedStream) Response() *model.Response {
	return s.inner.Response()
}

func (s *tracedStream) Finalize(primaryErr error) error {
	err := s.inner.Finalize(primaryErr)
	if err != nil {
		if telemetry.ShouldRecordSpanError(s.ctx, err) {
			s.span.RecordError(err)
			s.end(codes.Error, "stream finalize failed", false)
		} else {
			s.end(codes.Unset, "", false)
		}
		return err
	}
	s.end(codes.Ok, "finalized", true)
	return nil
}

func (s *tracedStream) end(code codes.Code, desc string, captureOutput bool) {
	s.endOnce.Do(func() {
		s.mu.Lock()
		usage := s.usage
		var (
			outputMessages []model.Message
			stopReason     string
			haveOutput     bool
		)
		if s.output != nil {
			outputMessages, stopReason, haveOutput = s.output.finish()
		}
		s.mu.Unlock()

		if (usage != model.TokenUsage{}) {
			s.span.SetAttributes(modelUsageAttrs(usage)...)
			s.span.AddEvent(
				"model.usage",
				"input_tokens", usage.InputTokens,
				"output_tokens", usage.OutputTokens,
				"total_tokens", usage.TotalTokens,
				"cache_read_tokens", usage.CacheReadTokens,
				"cache_write_tokens", usage.CacheWriteTokens,
			)
		}
		if captureOutput && haveOutput {
			s.applyOutputMessages(outputMessages, stopReason)
		}
		s.span.SetStatus(code, desc)
		s.span.End()
	})
}

func modelSpanAttrs(config modelTraceConfig, req *model.Request) []attribute.KeyValue {
	if req == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		telemetry.AttrGenAIOperationName.String(telemetry.GenAIOperationChat),
		telemetry.AttrGenAIRequestModel.String(requestedModelName(config, req)),
		attribute.String("loom_mcp.model_id", config.ModelID),
		attribute.String("loom_mcp.run_id", req.RunID),
		attribute.String("loom_mcp.model", req.Model),
		attribute.String("loom_mcp.model_class", string(req.ModelClass)),
		attribute.Bool("loom_mcp.stream", req.Stream),
		attribute.Bool("loom_mcp.thinking", req.Thinking != nil && req.Thinking.Enable),
		attribute.Int("loom_mcp.max_tokens", req.MaxTokens),
	}
	if config.ConversationID != "" {
		attrs = append(attrs, telemetry.AttrGenAIConversationID.String(config.ConversationID))
	}
	if config.AgentID != "" {
		attrs = append(attrs, telemetry.AttrGenAIAgentID.String(config.AgentID))
	}
	if config.AgentName != "" {
		attrs = append(attrs, telemetry.AttrGenAIAgentName.String(config.AgentName))
	}
	if req.MaxTokens > 0 {
		attrs = append(attrs, telemetry.AttrGenAIRequestMaxTokens.Int(req.MaxTokens))
	}
	if req.Temperature != 0 {
		attrs = append(attrs, telemetry.AttrGenAIRequestTemperature.Float64(float64(req.Temperature)))
	}
	return attrs
}

func requestedModelName(config modelTraceConfig, req *model.Request) string {
	if req != nil {
		if req.Model != "" {
			return req.Model
		}
		if req.ModelClass != "" {
			return string(req.ModelClass)
		}
	}
	return config.ModelID
}

func modelUsageAttrs(usage model.TokenUsage) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if usage.Model != "" {
		attrs = append(attrs, telemetry.AttrGenAIResponseModel.String(usage.Model))
	}
	if hasTokenUsageCounts(usage) {
		attrs = append(attrs, telemetry.GenAIUsageAttrs(
			usage.InputTokens,
			usage.OutputTokens,
			usage.CacheReadTokens,
			usage.CacheWriteTokens,
		)...)
	}
	return attrs
}

func hasTokenUsageCounts(usage model.TokenUsage) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.CacheReadTokens != 0 ||
		usage.CacheWriteTokens != 0
}

func (s *tracedStream) recordFirstChunk() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstChunkRecorded {
		return
	}
	s.firstChunkRecorded = true
	s.span.SetAttributes(telemetry.AttrGenAIResponseTTFT.Float64(time.Since(s.startedAt).Seconds()))
}

func isFirstGenAIOutputChunk(chunkType string) bool {
	switch chunkType {
	case model.ChunkTypeText,
		model.ChunkTypeThinking,
		model.ChunkTypeToolCall,
		model.ChunkTypeToolCallDelta:
		return true
	default:
		return false
	}
}

func (c *tracedClient) recordInputMessages(span telemetry.Span, req *model.Request) {
	if !c.config.CaptureGenAIMessages || req == nil {
		return
	}
	attr, ok, err := telemetry.GenAIInputMessagesAttr(sanitizeTraceInputMessages(req))
	setGenAIMessagesAttr(span, attr, ok, err, "input")
}

func (c *tracedClient) recordOutputMessages(span telemetry.Span, messages []model.Message, stopReason string) {
	if !c.config.CaptureGenAIMessages {
		return
	}
	attr, ok, err := telemetry.GenAIOutputMessagesAttr(messages, stopReason)
	setGenAIMessagesAttr(span, attr, ok, err, "output")
}

func (s *tracedStream) applyOutputMessages(messages []model.Message, stopReason string) {
	attr, ok, err := telemetry.GenAIOutputMessagesAttr(messages, stopReason)
	setGenAIMessagesAttr(s.span, attr, ok, err, "output")
}

func setGenAIMessagesAttr(span telemetry.Span, attr attribute.KeyValue, ok bool, err error, direction string) {
	if err != nil {
		span.AddEvent("gen_ai.messages_serialize_failed",
			"gen_ai.message.direction", direction,
			"exception.message", err.Error())
		return
	}
	if ok {
		span.SetAttributes(attr)
	}
}

func (s *tracedStream) recordOutputChunk(chunk model.Chunk) {
	if s.output == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output.recordChunk(chunk)
}

func responseOutputMessages(resp *model.Response, req *model.Request) []model.Message {
	if resp == nil {
		return nil
	}
	messages := sanitizeTraceOutputMessages(resp.Content, req)
	if len(resp.ToolCalls) == 0 {
		return messages
	}
	parts := make([]model.Part, 0, len(resp.ToolCalls))
	for _, call := range resp.ToolCalls {
		input := any(call.Payload)
		if call.Name == tools.ToolUnavailable {
			input = canonicalTraceToolUnavailableInput(req)
		}
		parts = append(parts, model.ToolUsePart{
			ID:    call.ID,
			Name:  string(call.Name),
			Input: input,
		})
	}
	messages = append(messages, model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: parts,
	})
	return messages
}

type genAIStreamAccumulator struct {
	parts          []model.Part
	stopReason     string
	availableTools []string
}

func newGenAIStreamAccumulator(req *model.Request) *genAIStreamAccumulator {
	return &genAIStreamAccumulator{availableTools: traceAvailableToolNames(req)}
}

func (a *genAIStreamAccumulator) recordChunk(chunk model.Chunk) {
	switch value := chunk.(type) {
	case model.TextChunk:
		a.appendOutputParts(value.Message.Parts)
	case model.ToolCallChunk:
		input := any(value.ToolCall.Payload)
		if value.ToolCall.Name == tools.ToolUnavailable {
			input = map[string]any{availableToolsKey: append([]string(nil), a.availableTools...)}
		}
		a.parts = append(a.parts, model.ToolUsePart{
			ID:    value.ToolCall.ID,
			Name:  string(value.ToolCall.Name),
			Input: input,
		})
	case model.StopChunk:
		a.stopReason = value.Reason
	}
}

func (a *genAIStreamAccumulator) finish() ([]model.Message, string, bool) {
	if len(a.parts) == 0 {
		return nil, a.stopReason, false
	}
	return []model.Message{{
		Role:  model.ConversationRoleAssistant,
		Parts: a.parts,
	}}, a.stopReason, true
}

func (a *genAIStreamAccumulator) appendOutputParts(parts []model.Part) {
	for _, part := range parts {
		if toolUse, ok := part.(model.ToolUsePart); ok && toolUse.Name == tools.ToolUnavailable.String() {
			toolUse.Input = map[string]any{availableToolsKey: append([]string(nil), a.availableTools...)}
			part = toolUse
		}
		text, ok := part.(model.TextPart)
		if !ok {
			a.parts = append(a.parts, part)
			continue
		}
		if text.Text == "" {
			continue
		}
		if len(a.parts) > 0 {
			if last, ok := a.parts[len(a.parts)-1].(model.TextPart); ok {
				last.Text += text.Text
				a.parts[len(a.parts)-1] = last
				continue
			}
		}
		a.parts = append(a.parts, text)
	}
}

func sanitizeTraceInputMessages(req *model.Request) []*model.Message {
	if req == nil || len(req.Messages) == 0 {
		return nil
	}
	messages := make([]*model.Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		if message == nil {
			messages = append(messages, nil)
			continue
		}
		copy := *message
		copy.Parts = sanitizeTraceParts(message.Parts, req)
		messages = append(messages, &copy)
	}
	return messages
}

func sanitizeTraceOutputMessages(messages []model.Message, req *model.Request) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, len(messages))
	for index := range messages {
		out[index] = messages[index]
		out[index].Parts = sanitizeTraceParts(messages[index].Parts, req)
	}
	return out
}

func sanitizeTraceParts(parts []model.Part, req *model.Request) []model.Part {
	if len(parts) == 0 {
		return nil
	}
	out := append([]model.Part(nil), parts...)
	for index, part := range out {
		toolUse, ok := part.(model.ToolUsePart)
		if !ok || toolUse.Name != tools.ToolUnavailable.String() {
			continue
		}
		toolUse.Input = canonicalTraceToolUnavailableInput(req)
		out[index] = toolUse
	}
	return out
}

func canonicalTraceToolUnavailableInput(req *model.Request) map[string]any {
	return map[string]any{availableToolsKey: traceAvailableToolNames(req)}
}

func traceAvailableToolNames(req *model.Request) []string {
	names := recoveryToolNames(req)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name != tools.ToolUnavailable.String() {
			out = append(out, name)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
