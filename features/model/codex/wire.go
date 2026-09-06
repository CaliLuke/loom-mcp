package codex

import "time"

const (
	codexProvider         = "codex"
	codexResponsesURL     = "https://chatgpt.com/backend-api/codex/responses"
	codexResponsesWSURL   = "wss://chatgpt.com/backend-api/codex/responses"
	defaultClientVersion  = "0.144.1"
	defaultIdleTimeout    = 5 * time.Minute
	maxStreamEventBytes   = 16 << 20
	maxHTTPErrorBodyBytes = 64 << 10
	codexUserAgent        = "loom-mcp/codex"
	sseBetaHeader         = "responses=experimental"
	webSocketBetaHeader   = "responses_websockets=2026-02-06"
	responsesLiteHeader   = "x-openai-internal-codex-responses-lite"
	responsesLiteMetadata = "ws_request_header_x_openai_internal_codex_responses_lite"
	wireForbiddenCode     = "forbidden"
	wireIncompleteEvent   = "response.incomplete"
	wireMessageItem       = "message"
	wireReasoningItem     = "reasoning"
	wireFunctionCallItem  = "function_call"
	interruptedToolOutput = "[No tool output recorded: the tool call was interrupted before it produced a result.]"
	maxOrphanOutputBytes  = 16_000
	maxOutputTokensReason = "max_output_tokens"
)
