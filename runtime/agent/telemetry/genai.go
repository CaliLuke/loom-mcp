package telemetry

import "go.opentelemetry.io/otel/attribute"

const (
	// GenAIOperationChat is the standard operation name for chat model calls.
	GenAIOperationChat = "chat"
)

const (
	// AttrGenAIConversationID identifies the conversation/session for a GenAI operation.
	AttrGenAIConversationID = attribute.Key("gen_ai.conversation.id")
	// AttrGenAIAgentID identifies the logical agent for a GenAI operation.
	AttrGenAIAgentID = attribute.Key("gen_ai.agent.id")
	// AttrGenAIAgentName names the logical agent for a GenAI operation.
	AttrGenAIAgentName = attribute.Key("gen_ai.agent.name")
	// AttrGenAIOperationName identifies the GenAI operation type.
	AttrGenAIOperationName = attribute.Key("gen_ai.operation.name")
	// AttrGenAIInputMessages captures sensitive input messages when explicitly enabled.
	AttrGenAIInputMessages = attribute.Key("gen_ai.input.messages")
	// AttrGenAIOutputMessages captures sensitive output messages when explicitly enabled.
	AttrGenAIOutputMessages = attribute.Key("gen_ai.output.messages")
	// AttrGenAIRequestModel records the requested model identifier or class.
	AttrGenAIRequestModel = attribute.Key("gen_ai.request.model")
	// AttrGenAIRequestMaxTokens records the requested max output token cap.
	AttrGenAIRequestMaxTokens = attribute.Key("gen_ai.request.max_tokens")
	// AttrGenAIRequestTemperature records the requested sampling temperature.
	AttrGenAIRequestTemperature = attribute.Key("gen_ai.request.temperature")
	// AttrGenAIResponseModel records the provider-resolved response model.
	AttrGenAIResponseModel = attribute.Key("gen_ai.response.model")
	// AttrGenAIResponseFinishReasons records provider finish reasons.
	AttrGenAIResponseFinishReasons = attribute.Key("gen_ai.response.finish_reasons")
	// AttrGenAIResponseTTFT records seconds to first streamed output chunk.
	AttrGenAIResponseTTFT = attribute.Key("gen_ai.response.time_to_first_chunk")
	// AttrGenAIUsageInputTokens records input token usage.
	AttrGenAIUsageInputTokens = attribute.Key("gen_ai.usage.input_tokens")
	// AttrGenAIUsageOutputTokens records output token usage.
	AttrGenAIUsageOutputTokens = attribute.Key("gen_ai.usage.output_tokens")
	// AttrGenAIUsageCacheReadTokens records cached input tokens read.
	AttrGenAIUsageCacheReadTokens = attribute.Key("gen_ai.usage.cache_read.input_tokens")
	// AttrGenAIUsageCacheCreationTokens records cached input tokens written.
	AttrGenAIUsageCacheCreationTokens = attribute.Key("gen_ai.usage.cache_creation.input_tokens")
)

// GenAIUsageAttrs returns standard token usage attributes for a model call.
func GenAIUsageAttrs(input, output, cacheRead, cacheCreation int) []attribute.KeyValue {
	return []attribute.KeyValue{
		AttrGenAIUsageInputTokens.Int(input),
		AttrGenAIUsageOutputTokens.Int(output),
		AttrGenAIUsageCacheReadTokens.Int(cacheRead),
		AttrGenAIUsageCacheCreationTokens.Int(cacheCreation),
	}
}
