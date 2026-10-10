# Runtime

Understand how the loom-mcp runtime orchestrates agents, enforces policies, and manages state.

## Provider errors and context overflow

`model.ProviderError` kinds are `auth`, `invalid_request`, `context_overflow`,
`rate_limited`, `unavailable`, and `unknown`. Use `errors.As` or
`model.AsProviderError` to inspect the kind, provider code, HTTP status, request
ID, and retryability when the adapter supplies them.

`ProviderErrorKindContextOverflow` (`context_overflow`) means the request exceeded
the model's context window. It is not retryable with the same request. Complete
returns an error instead of a response; a streaming overflow terminates the
stream without a canonical response, even if provisional chunks were delivered.

| Adapter | Overflow signal |
| --- | --- |
| Anthropic | HTTP 400 `invalid_request_error` with a message starting `prompt is too long`; `model_context_window_exceeded` in Complete or Stream. [Context-window documentation](https://platform.claude.com/docs/en/build-with-claude/context-windows). |
| Bedrock | `ValidationException` with `Input is too long for requested model`, `prompt is too long`, or `too many input tokens`, including receive-time stream errors; `model_context_window_exceeded` stops. Sources: [AWS validation guide](https://repost.aws/knowledge-center/bedrock-validation-exception-errors), [Claude context windows](https://platform.claude.com/docs/en/build-with-claude/context-windows), [captured Titan error](https://www.philschmid.de/amazon-titan-embeddings), [AWS stop reasons](https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages-request-response.html). |
| OpenAI | `context_length_exceeded` in SDK errors or streamed `response.failed` / `error` events. Sources: [Responses events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [OpenAI's recorded overflow fixture](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs). |
| Gemini / Vertex | HTTP 400 `INVALID_ARGUMENT` with `exceeds the maximum number of tokens`, with or without the input count. Sources: [captured SDK error](https://github.com/googleapis/python-genai/issues/2050), [message variant](https://github.com/google-gemini/gemini-cli/issues/22609), [Google API errors](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/api-errors). Streaming remains unsupported. |
| Codex | The code `context_length_exceeded` in HTTP errors or streamed `response.failed` / `error` events. Detection uses the code only and keeps the fixed redacted message. [Recorded provider fixture](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs). |
| Ollama | No documented chat overflow error is mapped. Chat truncates input to `num_ctx`; see [Ollama's prompt implementation](https://github.com/ollama/ollama/blob/main/server/prompt.go). |
| Gateway | No mapping: this transport-agnostic wrapper has no provider wire error contract. |

Bedrock's context-window stop no longer sets `Response.OutputLimited` or triggers
`OutputBounds` recovery (the shorter-answer retry with tools disabled).
Anthropic's `model_context_window_exceeded` stop previously returned a successful
`Response` with that `StopReason`; it now fails with `context_overflow`.
`max_tokens` still means output-limited. Until overflow recovery lands in K14,
`context_overflow` fails the planner activity; K9 adds no recovery.
Other errors keep their existing behavior: ordinary Bedrock and Codex 400s remain
`invalid_request`; Anthropic, OpenAI, Gemini, and Ollama retain their existing
error wrapping rather than gaining new classifications.


## Architecture Overview

The loom-mcp runtime orchestrates the plan/execute/resume loop, enforces policies, manages state, and coordinates with engines, planners, tools, memory, hooks, and feature modules.

| Layer | Responsibility |
| --- | --- |
| DSL + Codegen | Produce agent registries, tool specs/codecs, workflows, MCP adapters |
| Runtime Core | Orchestrates plan/start/resume loop, policy enforcement, hooks, memory, streaming |
| Workflow Engine Adapter | Temporal adapter implements engine.Engine; other engines can plug in |
| Feature Modules | Optional integrations (MCP, Pulse, Mongo stores, model providers) |

## High-Level Agentic Architecture

- Agents: Orchestrators identified by `agent.Ident`
- Runs: Identified by RunID and grouped by SessionID/TurnID
- Toolsets & tools: Named tool capabilities
- Planners: implement `PlanStart` / `PlanResume`
- Run tree & agent-as-tool: child runs for nested agents
- Session-owned streams: typed events with explicit end marker

## Quick Start

```go
package main

import (
    "context"

    chat "example.com/assistant/gen/orchestrator/agents/chat"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func main() {
    rt := runtime.New()
    ctx := context.Background()
    err := chat.RegisterChatAgent(ctx, rt, chat.ChatAgentConfig{Planner: newChatPlanner()})
    if err != nil { panic(err) }

    if _, err := rt.CreateSession(ctx, "session-1"); err != nil { panic(err) }

    client := chat.NewClient(rt)
    out, err := client.Run(ctx, "session-1", []*model.Message{{
        Role: model.ConversationRoleUser,
        Parts: []model.Part{model.TextPart{Text: "Summarize the latest status."}},
    }})
    if err != nil { panic(err) }
}
```

## Client-Only vs Worker

Client-only: call runs with `runtime.WithEngine(temporalClient)` and a generated client.  
Worker-only: register agents and run engine worker loop.

## Plan → Execute → Resume Loop

1. Start workflow, record context (`RunID`, `SessionID`, `TurnID`, labels, caps)
2. `PlanStart`
3. Tool calls scheduled
4. `PlanResume` with tool results until final response or policy stop
5. Hooks and stream events emitted for each step

Each plan/resume step is one logical planner turn backed by a retryable workflow
activity. Generated registrations allow up to three attempts by default, so a
late attempt failure can repeat model calls or planner-owned side effects.
Planners must be retry-safe and protect non-idempotent external effects with
stable idempotency keys. `RetryHint` handles tool-failure recovery inside a
successful planner result; it is not an activity-retry instruction.

## Run Phases

`prompted`, `planning`, `executing_tools`, `synthesizing`, `completed`, `failed`, `canceled`

A typical successful progression:

`prompted -> planning -> executing_tools -> planning -> synthesizing -> completed`

`run.Phase` is the fine-grained loop state. `run.Status` is terminal lifecycle.

## Policies, Caps, and Labels

Design-time policy is configured in `RunPolicy(...)`.

Runtime overrides with `rt.OverridePolicy(agentID, runtime.RunPolicy{...})` (local to process only).

`policy.CapsState` carries tool-call and consecutive-failure counters. Its
legacy `ExpiresAt` field is deprecated and ignored. Use `TimeBudget` for the
deterministic active-work deadline; external waits pause that budget.

## Tool Execution

- Service-native toolsets: runtime decodes payloads and dispatches to toolset executors.
- Agent-as-tool: executes provider agent as child run.
- MCP toolsets: forward canonical JSON through an SDK-backed runtime caller or
  a generated in-process local provider.

## Prompt runtime contracts

Runtime prompt flow:

- `PromptRegistry` stores specs
- `WithPromptStore` adds override store
- planner renders prompt via `RenderPrompt(...)`
- `PromptRefs` attached on model requests

## Memory, Streaming, Telemetry

- Hook bus publishes structured events (run lifecycle, phases, tool events, awaits, thinking)
- `memory.Store`, `runlog.Store`, `stream.Sink`
- Stream profile controls visibility by audience

## Tool call display hints

`CallHintTemplate` / `ResultHintTemplate` become `DisplayHint` and can be overridden in runtime.

## Engine abstraction

In-memory for dev, Temporal for production durability and retries.

## Pause & Resume

`rt.PauseRun(...)` and `rt.ResumeRun(...)`, with `run_paused` / `run_resumed` events.

## Tool Confirmation

`Confirmation(...)` (DSL) and `runtime.WithToolConfirmation(...)` (runtime override) implement await/decision.

Decision payload carries `run_id`, `id`, `approved`, `requested_by`.

## LLM integration

Planners use model clients from context:

- `Complete(ctx, *model.Request)`
- `Stream(ctx, *model.Request)`

Decorated clients auto-emit events for assistant chunks/thinking/usage.

## Next steps

- Learn Toolsets
- Runtime streaming and policies
- Memory, sessions, and persistence
