// Package runtime provides the goa-ai runtime implementation.
//
// This file contains agent-as-tool support: a toolset registration that executes a
// nested agent run and adapts its canonical run output into a parent tool_result.
//
// Contract highlights:
//   - The nested run context always carries the canonical JSON tool payload as
//     RunContext.ToolArgs for provider planners to decode once and render
//     method-specific prompts.
//   - Consumer-side prompt rendering (PromptSpecs/Templates/Texts) is optional and
//     must be payload-only: it cannot depend on provider-only server context.
//   - When no consumer-side prompt content is configured, the runtime uses the
//     canonical tool payload to construct the nested user message deterministically.
package runtime
