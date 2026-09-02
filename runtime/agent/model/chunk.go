package model

// Chunk is one closed model-stream event. Only the value variants declared in
// this package are accepted by the validated stream boundary.
type Chunk interface {
	// Kind identifies the event variant.
	Kind() string

	isChunk()
}

// TextChunk carries one incremental assistant message.
type TextChunk struct {
	Message Message
}

// ThinkingChunk carries one incremental assistant thinking message.
type ThinkingChunk struct {
	Message Message
}

// ToolCallChunk carries one complete model tool call.
type ToolCallChunk struct {
	ToolCall ToolCall
}

// ToolCallDeltaChunk carries one incremental tool-call payload fragment.
type ToolCallDeltaChunk struct {
	Delta ToolCallDelta
}

// CompletionChunk carries one canonical structured completion payload.
type CompletionChunk struct {
	Completion Completion
}

// CompletionDeltaChunk carries one provisional structured-output fragment.
type CompletionDeltaChunk struct {
	Delta CompletionDelta
}

// UsageChunk carries one incremental token-usage record.
type UsageChunk struct {
	Usage TokenUsage
}

// StopChunk terminates a model stream.
type StopChunk struct {
	Reason        string
	OutputLimited bool
}

// Kind returns the text event discriminator.
func (TextChunk) Kind() string { return ChunkTypeText }

// Kind returns the thinking event discriminator.
func (ThinkingChunk) Kind() string { return ChunkTypeThinking }

// Kind returns the tool-call event discriminator.
func (ToolCallChunk) Kind() string { return ChunkTypeToolCall }

// Kind returns the tool-call-delta event discriminator.
func (ToolCallDeltaChunk) Kind() string { return ChunkTypeToolCallDelta }

// Kind returns the completion event discriminator.
func (CompletionChunk) Kind() string { return ChunkTypeCompletion }

// Kind returns the completion-delta event discriminator.
func (CompletionDeltaChunk) Kind() string { return ChunkTypeCompletionDelta }

// Kind returns the usage event discriminator.
func (UsageChunk) Kind() string { return ChunkTypeUsage }

// Kind returns the terminal stop event discriminator.
func (StopChunk) Kind() string { return ChunkTypeStop }

func (TextChunk) isChunk()            {}
func (ThinkingChunk) isChunk()        {}
func (ToolCallChunk) isChunk()        {}
func (ToolCallDeltaChunk) isChunk()   {}
func (CompletionChunk) isChunk()      {}
func (CompletionDeltaChunk) isChunk() {}
func (UsageChunk) isChunk()           {}
func (StopChunk) isChunk()            {}
