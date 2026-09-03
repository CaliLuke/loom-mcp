# loom-mcp Runtime Reference

The `loom-mcp` runtime is the execution engine that turns your agent designs
into running systems. Its Go module path is
`github.com/CaliLuke/loom-mcp/v2`; this document uses `loom-mcp` for product
discussion and the versioned module path in code examples.

## When to Use This Guide

Read this guide when you need to:

- Bootstrap a runtime for your agents
- Understand the plan → execute → resume loop
- Configure policy enforcement, memory, and streaming
- Implement custom planners or tool executors
- Debug agent behavior or performance issues

For design-time DSL concepts, see [`docs/dsl.md`](dsl.md). For a high-level system
overview, see [`docs/overview.md`](overview.md).

---

## Mental Model

The runtime operates on three layers:

```text
┌─────────────────────────────────────────────────────────────────────┐
│                        Application Layer                            │
│  Services call generated clients to start runs and stream events    │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────┐
│                         Runtime Layer                               │
│  Orchestrates: Planners ↔ Tools ↔ Memory ↔ Hooks ↔ Policy           │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────┐
│                         Engine Layer                                │
│  Provides durable execution: Temporal, in-memory, or custom         │
└─────────────────────────────────────────────────────────────────────┘
```

**Key concepts:**

| Concept     | Purpose                                                                                             |
| ----------- | --------------------------------------------------------------------------------------------------- |
| **Runtime** | Central registry and coordinator. Holds agents, toolsets, models, hooks, and stores.                |
| **Engine**  | Workflow backend (Temporal or in-memory). Provides durable execution, activities, and signals.      |
| **Planner** | Decision-maker. Analyzes messages and returns tool calls or a final response.                       |
| **Toolset** | Collection of tools with shared execution logic. Generated from DSL or registered manually.         |
| **Hooks**   | Internal event bus. Publishes lifecycle events for memory, streaming, and telemetry.                |
| **Stream**  | External event delivery. Transforms hook events into client-facing updates (SSE, WebSocket, Pulse). |

---

## Quick Start

### Minimal Example

```go
package main

import (
    "context"
    "fmt"

    chat "example.com/assistant/gen/orchestrator/agents/chat"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func main() {
    // 1. Create runtime (in-memory engine by default)
    rt := runtime.New()

    // 2. Register agent with a planner
    if err := chat.RegisterChatAgent(context.Background(), rt, chat.ChatAgentConfig{
        Planner: &MyPlanner{},
    }); err != nil {
        panic(err)
    }

    // 3. Create typed client and run
    client := chat.NewClient(rt)
    out, err := client.Run(context.Background(), "session-1", []*model.Message{{
        Role:  model.ConversationRoleUser,
        Parts: []model.Part{model.TextPart{Text: "Hello!"}},
    }})
    if err != nil {
        panic(err)
    }

    fmt.Println("Response:", out.Final)
}
```

### Production Configuration

```go
func main() {
    // Temporal engine for durable workflow execution
    temporalEng, _ := temporal.NewWorker(temporal.Options{
        ClientOptions: &client.Options{HostPort: "temporal:7233"},
        WorkerOptions: temporal.WorkerOptions{TaskQueue: "orchestrator.chat"},
    })
    defer temporalEng.Close()

    // MongoDB stores for persistence. Construct these with the adapters under
    // features/{memory,runlog,session}/mongo.
    mongoClient := newMongoClient()
    memStore := memorymongo.New(mongoClient)
    runlogStore := newMongoRunlogStore(mongoClient)
    sessionStore := newMongoSessionStore(mongoClient)

    // Pulse sink for real-time streaming
    pulseSink, _ := pulse.NewSink(pulse.Options{Client: newPulseClient()})

    // Construct runtime with all features
    rt := runtime.New(
        runtime.WithEngine(temporalEng),
        runtime.WithMemoryStore(memStore),
        runtime.WithRunEventStore(runlogStore),
        runtime.WithSessionStore(sessionStore),
        runtime.WithStream(pulseSink),
        runtime.WithPolicy(basicpolicy.New()),
        runtime.WithLogger(telemetry.NewClueLogger()),
        runtime.WithMetrics(telemetry.NewClueMetrics()),
        runtime.WithTracer(telemetry.NewClueTracer()),
    )

    // Register toolsets first, then agents, then seal registration.
    if err := chat.RegisterChatAgent(ctx, rt, chat.ChatAgentConfig{
        Planner:      newChatPlanner(),
        HistoryModel: smallModelClient, // for history compression
    }); err != nil {
        panic(err)
    }
    if err := rt.Seal(ctx); err != nil {
        panic(err)
    }

    // Workers poll and execute; clients submit runs from anywhere
}
```

Temporal persists workflow history, including the live workflow ledger, but it
does not persist the runtime's other stores. If `WithRunEventStore` and
`WithSessionStore` are omitted, `runtime.New` uses process-local in-memory
stores: workflow replay still works, while canonical run introspection and
session/run metadata can disappear at process restart. Use shared persistent
stores in every worker and client-only process that reads or writes those
surfaces. Configure `memory.Store` and `stream.Sink` separately when their
derived projection or delivery state must also survive worker replacement.

### Seal semantics

`rt.Seal(ctx)` is a real activation boundary, not a pure no-op. On worker-mode
engines it:

- Closes registration so any later `RegisterAgent`, `RegisterToolset`, or
  `RegisterModel` call fails with `ErrRegistrationClosed`. Register and replace
  model clients before sealing; post-seal hot-swapping is intentionally not a
  runtime contract. Closure happens even if activation later fails, so partial
  bring-up cannot smuggle handlers or model clients onto a sealed runtime.
- Activates every staged Temporal worker by calling `worker.Start()` with
  retries until activation succeeds or `ctx` ends.
- Returns the activation error verbatim (queue-qualified) when `ctx` ends
  before the worker accepts. Callers may retry `Seal` with a fresh context;
  successful retries are idempotent.
- Surfaces process-fatal worker failures through `worker.OnFatalError` rather
  than crashing the goroutine — chain your own callback if you need to escalate.

---

## Runtime Configuration

### Construction Options

Create a runtime using `runtime.New()` with functional options:

```go
rt := runtime.New(
    runtime.WithEngine(engine),          // Workflow backend (required for production)
    runtime.WithMemoryStore(store),      // Transcript persistence
    runtime.WithMemorySearcher(searcher),// Indexed/cross-run memory lookup
    runtime.WithPromptStore(promptStore),// Scoped prompt overrides
    runtime.WithStream(sink),            // Real-time event streaming
    runtime.WithPolicy(engine),          // Policy enforcement
    runtime.WithHooks(bus),              // Custom event bus (rare)
    runtime.WithLogger(logger),          // Structured logging
    runtime.WithMetrics(metrics),        // Counter/histogram recording
    runtime.WithTracer(tracer),          // Distributed tracing
    runtime.WithWorker(agentID, cfg),    // Per-agent queue placement
)
```

### Local Debug Server

`runtime/agent/debug` provides an explicit development-only HTTP server for
local run inspection:

```go
srv, err := debug.NewServer(debug.Config{
    Runtime: rt,
    Addr:    "127.0.0.1:0",
})
if err != nil {
    return err
}
if err := srv.Start(); err != nil {
    return err
}
defer srv.Shutdown(context.Background())
```

The server is disabled by default and is not generated into service, MCP, or
agent APIs. The default bind address is `127.0.0.1:0`. It returns JSON
envelopes for run snapshots, run events, await state, memory, artifacts, and
workflow event counts under `/runs/{id}`.

When options are omitted, the runtime uses sensible defaults:

| Option                | Default                                                |
| --------------------- | ------------------------------------------------------ |
| Engine                | In-memory (synchronous, non-durable)                   |
| MemoryStore           | None (transcripts not persisted)                       |
| MemorySearcher        | None (indexed memory search unavailable)               |
| PromptStore           | None (baseline prompt specs only, no scoped overrides) |
| Stream                | None (no external event delivery)                      |
| Policy                | None (all tools allowed, caps from agent registration) |
| Hooks                 | In-process bus                                         |
| Logger/Metrics/Tracer | No-op implementations                                  |

`runtime.WithWorker(...)` is intentionally narrow: it controls agent placement
(`Queue`) only. Semantic planner and tool attempt budgets come from the DSL
(`RunPolicy.Timing`) or per-run overrides (`runtime.WithTiming(...)`). If you
use the Temporal engine and need queue-wait or liveness tuning, configure those
mechanics on `temporal.Options.ActivityDefaults` when constructing the engine.

### Prompt Registry and Overrides

The runtime always initializes `Runtime.PromptRegistry`. Prompt management has two layers:

- **Baseline specs**: register immutable `prompt.PromptSpec` definitions in memory.
- **Scoped overrides**: optionally resolve arbitrary label and session scopes
  through `prompt.Store` (`runtime.WithPromptStore(...)`). Matching session
  scopes outrank non-session scopes; more matching labels outrank fewer; equal
  specificity uses the newest override.

The Mongo adapter persists a versioned SHA-256 `scope_fingerprint` and performs
at most one indexed session lookup plus one indexed global lookup, each limited
to the most specific/newest record. Mongo scopes accept at most 15 labels so
the exact matching-subset set remains bounded. On startup the Mongo client
backfills missing `scope_fingerprint` values from `scope_labels` before creating
the compound index; migration errors and legacy scopes above the bound fail
construction rather than silently hiding overrides. Normal resolution remains
index-only.

This registry is the internal runtime prompt system Loom uses while executing planners and agents.
Registering a prompt here makes it available to Loom runtime code. MCP clients see prompts declared
with `StaticPrompt(...)` and `DynamicPrompt(...)`, which expose `prompts/list` and `prompts/get` on
generated MCP servers. A single-message `StaticPrompt(...)` can opt into both surfaces with
`RuntimePrompt(...)`; generated MCP packages then expose `RegisterRuntimePrompts(reg)` to register
matching baseline `prompt.PromptSpec` values from the same design declaration.

Generated MCP servers also expose design-declared `SkillDirectory(...)` roots as
`skill://` resources. Use that when MCP clients should discover local agent
skills through `resources/list` and read `SKILL.md`, `_manifest`, or supporting
files through `resources/read`.

```go
import (
    mcpassistant "example.com/assistant/gen/mcp_assistant"
    promptmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo"
    clientmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo/clients/mongo"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

mongoClient, _ := clientmongo.New(clientmongo.Options{
    Client:     rawMongoClient, // *mongo.Client from go.mongodb.org/mongo-driver/v2/mongo
    Database:   "aura",
    Collection: "prompt_overrides",
})
promptStore, _ := promptmongo.NewStore(mongoClient)

rt := runtime.New(
    runtime.WithPromptStore(promptStore),
)

_ = rt.PromptRegistry.Register(prompt.PromptSpec{
    ID:       "aura.chat.system",
    AgentID:  "orchestrator.chat",
    Role:     prompt.PromptRoleSystem,
    Template: "You are {{ .AssistantName }}.",
})

// For design-declared shared MCP/runtime prompts:
_ = mcpassistant.RegisterRuntimePrompts(rt.PromptRegistry)
```

Render prompts from planners through `PlannerContext.RenderPrompt(...)`. The result includes rendered
text and a versioned `PromptRef` for provenance.

### Two Deployment Patterns

**Worker process** — Registers agents and executes workflows:

```go
rt := runtime.New(runtime.WithEngine(temporalWorker))

// Register agents with planners
if err := chat.RegisterChatAgent(ctx, rt, chat.ChatAgentConfig{
    Planner: myPlanner,
}); err != nil {
    panic(err)
}

// Workers poll task queues and execute runs
```

**Client-only process** — Submits runs without local execution:

```go
rt := runtime.New(runtime.WithEngine(temporalClient))

// No registration needed; use generated client with route info
client := chat.NewClient(rt)
out, err := client.Run(ctx, "session-1", msgs)
```

The generated `NewClient` function embeds the route (workflow name, task queue) so
client-only processes can submit runs to remote workers.

Worker and client-only runtimes do not share in-memory defaults. In a
multi-process deployment, give both runtimes the same persistent
`WithRunEventStore(...)` and `WithSessionStore(...)` implementations. Temporal
history is the workflow-recovery authority; it is not a replacement for the
runlog, session store, transcript projection, or client stream.

---

## The Plan → Execute → Resume Loop

Every agent run follows this lifecycle:

```text
Start ──► PlanStart ──► Tool Calls? ──► Execute Tools ──► PlanResume ──► ...
                │                                              │
                │                                              │
                └──► Final Response ◄──────────────────────────┘
```

1. **Start** — `client.Run()` or `client.Start()` creates a workflow
2. **PlanStart** — Planner receives messages and decides: answer or call tools?
3. **Execute** — Tools run as activities (parallel by default)
4. **PlanResume** — Planner receives tool results and decides next step
5. **Repeat** — Loop continues until planner returns a `FinalResponse`

### Workflow Contracts

- **SessionID is required.** `Start` fails fast if `SessionID` is empty.
- **Agents must register before runs start.** Registration closes after the first
  run to maintain worker determinism.
- **Tool results flow through codecs.** The runtime decodes results centrally and
  provides typed values to planners and hooks.

### Tool payload codecs and defaults (Feature)

Tool payloads are decoded using a two-step model shared by the generated transport/codegen layer:

1. **Decode JSON into a helper “decode‑body” type** with pointer fields, so the codec can
   distinguish **missing** from **zero** and return precise validation issues.
2. **Transform helper → final payload** using `codegen.GoTransform`.

For tool payloads, the generated payload struct uses **default‑aware field shapes**:
optional primitives with defaults become **values** (non‑pointers). During step (2), the transform
generator injects defaults when helper fields are nil.

This is a hard codegen contract: any generated transforms that read tool payload fields must use
matching AttributeContext default semantics, or the generated code may contain invalid nil checks or
assignments and fail to compile.

See [`docs/tool_payload_defaults.md`](tool_payload_defaults.md) for the full contract.

---

## Planner Contract

Planners implement the decision logic for agents. The runtime invokes planners through
activities and feeds results back into the workflow loop.

### Interface

```go
type Planner interface {
    PlanStart(ctx context.Context, input *PlanInput) (*PlanResult, error)
    PlanResume(ctx context.Context, input *PlanResumeInput) (*PlanResult, error)
}
```

**PlanStart** receives the initial messages; **PlanResume** receives messages plus
recent tool results. Both return a `PlanResult` containing tool calls, a final
response, or an await request.

`PlanStart` and `PlanResume` are workflow activities, so “one planner turn” does
not imply one Go method invocation. Generated registrations use an infrastructure
retry policy with up to three attempts by default. If an attempt fails after a
model call, event hook, or other side effect, the engine may invoke the same
planner method again with the same logical input. Planner implementations must be
retry-safe: avoid direct non-idempotent side effects, or protect them with stable
idempotency keys. A returned error fails the current activity attempt; the run
fails only after the configured retries are exhausted or the error is
non-retryable. `PlanResult.RetryHint` is successful planner output for recovering
from tool failures and does not control activity retries.

### PlanInput and PlanResumeInput

```go
type PlanInput struct {
    Messages   []*model.Message      // Conversation history
    RunContext run.Context           // Run-level identifiers and labels
    Agent      PlannerContext        // Runtime services (memory, models, reminders)
    Events     PlannerEvents         // Streaming event emitter
    Reminders  []reminder.Reminder   // Active system reminders
}

type PlanResumeInput struct {
    Messages    []*model.Message
    RunContext  run.Context
    Agent       PlannerContext
    Events      PlannerEvents
    ToolOutputs []*ToolOutput         // Results from previous tool calls
    Finalize    *Termination          // Non-nil when runtime forces finalization
    Reminders   []reminder.Reminder
}
```

### PlanResult

```go
type PlanResult struct {
    ToolCalls     []ToolRequest    // Tools to execute (empty for final response)
    FinalResponse *FinalResponse   // Terminal assistant message
    Streamed      bool             // True if text was already streamed via Events
    Await         *Await           // Pause for clarification or external tools
    RetryHint     *RetryHint       // Guidance after tool failures
    Notes         []PlannerAnnotation
}
```

### PlannerContext

`PlannerContext` provides read-only access to runtime services:

```go
type PlannerContext interface {
    ID() agent.Ident                      // Agent identifier
    RunID() string                        // Current run identifier
    Memory() memory.Reader                // Read prior turn history
    Logger() telemetry.Logger             // Structured logging
    Metrics() telemetry.Metrics           // Counters and histograms
    Tracer() telemetry.Tracer             // Distributed tracing
    State() AgentState                    // Ephemeral per-run key-value store
    ModelClient(id string) (model.Client, bool)  // LLM client lookup
    RenderPrompt(ctx context.Context, id string, data any) (*prompt.PromptContent, error)
    AddReminder(r reminder.Reminder)      // Register backstage guidance
    RemoveReminder(id string)             // Clear a reminder
}
```

### PlannerEvents

`PlannerEvents` emits streaming updates that the runtime captures and publishes:

```go
type PlannerEvents interface {
    AssistantChunk(ctx context.Context, text string)
    PlannerThinkingBlock(ctx context.Context, block model.ThinkingPart)
    PlannerThought(ctx context.Context, note string, labels map[string]string)
    UsageDelta(ctx context.Context, usage model.TokenUsage)
}
```

### Deterministic Workflow Composition

For fixed, model-free coordination, generated workflows use planner implementations
instead of bypassing the runtime loop.

`planner.NewSequentialWorkflowPlanner(...)` remains the source-compatible path for
plain `Workflow` plus `Step` declarations. It emits authored tool steps one at a
time.

`planner.NewGraphWorkflowPlanner(...)` powers graph workflows with parallel nodes,
join barriers, typed human-input nodes, branch targets, and bounded loops. It
derives tool completion from stable node/tool-call IDs in accumulated
`ToolOutputs`, and typed-input completion from `PlanResumeInput.TypedInputs`, so
resuming after partial parallel completion schedules only the remaining ready nodes.
Branch selection deactivates the entire unselected path: a nested branch on a
not-taken path never resolves, and its own targets are skipped transitively,
while a node that is also the target of a selected branch still runs.

Typed human-input nodes emit `AwaitTypedInput` and resume through
`Runtime.ProvideTypedInput`; typed answers are kept separate from tool execution
history.

Cross-layer changes that span the agent DSL, generated registration, and runtime
behavior should extend `integration_tests/fixtures/agent_features`. That fixture
is the acceptance proof for model-facing artifacts, memory, skills, interceptors,
retry-and-reflect, workflow graphs, typed input, and debug endpoint visibility;
package-level tests remain the owner for narrower DSL, codegen, planner, runtime
toolset, and debug-server contracts.

---

## Streaming Planners

When using model streaming, planners have two options for emitting events. Choose
**exactly one** per stream to avoid double-emitting.

### Option 1: Runtime-Decorated Client (Recommended)

`PlannerContext.ModelClient(id)` returns a client wrapped with an event decorator.
The decorator emits `AssistantChunk`, `PlannerThinkingBlock`, and `UsageDelta`
automatically on each `Recv()` call:

```go
func (p *MyPlanner) PlanResume(ctx context.Context, input *PlanResumeInput) (*PlanResult, error) {
    mc, ok := input.Agent.ModelClient("bedrock")
    if !ok {
        return nil, errors.New("model not configured")
    }

    req := &model.Request{
        RunID:      input.RunContext.RunID,
        ModelClass: model.ModelClassHighReasoning,
        Messages:   input.Messages,
        Stream:     true,
    }

    st, err := mc.Stream(ctx, req)
    if err != nil {
        return nil, err
    }
    // Drain stream manually; events are emitted automatically by the wrapper.
    var calls []ToolRequest
    var out strings.Builder
    for {
        chunk, rerr := st.Recv()
        //nolint:errorlint // Only literal EOF proves validated completion.
        if rerr == io.EOF {
            break
        }
        if rerr != nil {
            return nil, st.Finalize(rerr)
        }
        switch chunk := chunk.(type) {
        case model.ToolCallChunk:
            calls = append(calls, ToolRequest{
                Name:       chunk.ToolCall.Name,
                Payload:    chunk.ToolCall.Payload,
                ToolCallID: chunk.ToolCall.ID,
            })
        case model.TextChunk:
            // Accumulate text locally (already emitted via decorator)
            for _, p := range chunk.Message.Parts {
                if tp, ok := p.(model.TextPart); ok {
                    out.WriteString(tp.Text)
                }
            }
        }
    }
    if err := st.Finalize(nil); err != nil {
        return nil, err
    }

    if len(calls) > 0 {
        return &PlanResult{ToolCalls: calls}, nil
    }
    return &PlanResult{
        FinalResponse: &FinalResponse{
            Message: &model.Message{
                Role:  model.ConversationRoleAssistant,
                Parts: []model.Part{model.TextPart{Text: out.String()}},
            },
        },
        Streamed: true, // Text was already streamed
    }, nil
}
```

The decorated stream advertises that it owns runtime events, so it is also safe
to pass it to `planner.ConsumeStream`; the helper only aggregates its result.

### Option 2: ConsumeStream without Runtime Event Decoration

When you have a validated `model.Client` that is not decorated to emit runtime
events, use `planner.ConsumeStream`:

```go
sum, err := planner.ConsumeStream(ctx, streamer, input.Events)
if err != nil {
    return nil, err
}
```

This helper drains the stream, emits events via `PlannerEvents` when the stream
does not own them, and returns a `StreamSummary` with accumulated text and tool
calls. Runtime planner events apply the same provisional presentation lifecycle
to this validated-but-undecorated path.

### Typed Structured Completions

`runtime/agent/completion` uses a validated `model.Client` with a generated
typed completion spec. The helper clones the request, installs
`model.StructuredOutput`, rejects tool calls on that request, and binds the
generated decoder into the immutable request contract. Schema-valid output that
the generated decoder rejects never leaves the client boundary:

```go
resp, err := completion.Complete(ctx, modelClient, req, completions.SpecDraft)
if err != nil {
    return err
}
fmt.Println(resp.Value)
```

For streaming completions, `completion.Stream` returns a
`model.ValidatedStreamer` that
allows `completion_delta`, thinking, and usage chunks, but withholds the
canonical `completion` until the complete stream is accepted. Decode the final
typed value with `completion.DecodeChunk`, drain to the literal `io.EOF`, and
finalize the stream.

The consumer must observe literal EOF before reading `Response` or finalizing
successfully. The first `Finalize` result is authoritative and includes provider
cleanup. Runtime tracing and adaptive rate limiting commit their lifecycle
outcome there rather than when `Recv` first reaches EOF.

Runtime-owned model streams also publish provisional presentation events. A
`model_presentation` event starts the presentation. Each live assistant or
thinking delta carries its `presentation_id`. Stream finalization stages valid
content, and the surrounding planner activity emits exactly one `accepted` or
`discarded` state. Treat accepted content as canonical and remove discarded
content. Partial tool-call JSON stays inside the model boundary until the
complete call passes validation. The runtime emits `accepted` only after the
planner succeeds and one atomic canonical response event for all ready
presentations is durable. That assistant-turn event carries the presentation
IDs and authoritative response messages. A later planner failure or failed
run-log write discards every presentation and fails the attempt. When a final
planner response matches an accepted presentation, the runtime suppresses the
legacy duplicate final-message path even if the planner omitted `Streamed`.
Canonical presentation commits support the full validated model-output bound;
the smaller ordinary-hook payload limit does not truncate accepted content.

---

## Tool Execution

### Tool Payload and Result Flow

1. **Model emits tool call** — Provider adapter produces `model.ToolCall` with
   `jsontext.Value` payload
2. **Planner returns ToolRequest** — Payload stays as `jsontext.Value`
3. **Runtime decodes payload** — Uses generated codecs to validate and decode
4. **Executor runs tool** — Receives typed or raw payload depending on configuration
5. **Runtime encodes result** — Uses generated codecs for consistency
6. **Planner receives ToolResult** — Gets typed result via `ToolResult.Result`

### ToolsetRegistration

Toolsets bundle execution logic for a group of tools:

```go
type ToolsetRegistration struct {
    Name        string                     // Qualified identifier (service.toolset)
    Description string                     // Human-readable context
    Metadata    policy.ToolMetadata        // Policy metadata
    Execute     func(ctx, *ToolRequest) (*ToolExecutionResult, error)  // Dispatcher
    Specs       []tools.ToolSpec           // JSON codecs and schemas
    TaskQueue   string                     // Optional queue override
    Inline      bool                       // Execute in workflow context
    CallHints   map[tools.Ident]*template.Template   // Tool call DisplayHint templates (typed payload only)
    ResultHints map[tools.Ident]*template.Template   // Tool result preview templates (typed result only)
    PayloadAdapter func(...)               // Pre-decode transformation
    ResultAdapter  func(...)               // Post-encode transformation
    AgentTool   *AgentToolConfig           // Agent-as-tool configuration
}
```

### Tool Call Display Hints (DisplayHint)

The runtime can surface user-facing hints for tool calls (for example in UIs) via the `DisplayHint` field on
hook + stream events.

Contract:

- Hook constructors do not render hints. Tool call scheduled events default to `DisplayHint==""`.
- The runtime may enrich and persist a **durable default** hint at publish time by decoding the typed tool
  payload using generated codecs and executing the `CallHintTemplate` (if registered).
- When typed decoding fails or no template is registered, the runtime leaves `DisplayHint` empty. Hints are
  never rendered against raw JSON bytes.
- If a producer explicitly sets `DisplayHint` (non-empty) before publishing the hook event, the runtime treats
  it as authoritative and does not overwrite it.

For per-consumer wording changes, configure `runtime.WithHintOverrides` on the runtime. Overrides take precedence
over DSL-authored templates for streamed `tool_start` events.

### Tool Implementation Patterns

**Method-backed tools** — Generated from `BindTo` DSL:

```go
// Generated code maps tool payloads to service method calls
reg := helpers.NewHelpersToolsetRegistration(serviceClient)
rt.RegisterToolset(reg)
```

For method-backed toolset tools, codegen emits a shared
`Dispatch<Tool>Method(ctx, meta, raw, labels, opts)` helper in the owning
toolset package. Runtime service executors call that dispatcher instead of
duplicating the default `BindTo(...)` path. The dispatcher owns payload decode,
tool-to-method transform, label and metadata injection, interceptor wrapping,
method invocation, method-to-tool transform, retry hints, bounds projection, and
server-data projection.

When a method-backed toolset tool is also exposed to MCP with
`Expose(AgentRuntime, MCPSurface)` and `MCPPlacement(...)`, generated MCP
adapters call the same dispatcher. Design-time exposure only permits the
surface; runtime deployment still controls toolset registration and MCP server
construction separately.

### Registry-Routed Provider Execution (Service-Side)

Loom MCP supports cross-process tool invocation via the **Internal Tool Registry**. In this mode:

- The registry validates payload JSON, atomically admits or rejects one durable
  run/tool-call identity, and publishes admitted calls to
  `toolset:<toolsetID>:requests`.
- A provider loop claims the exact admitted call before execution. It submits
  deltas and terminal results through the registry, which owns canonical
  publication on `result:<toolUseID>`.

For method-backed service toolsets, codegen emits a provider adapter at:

- `gen/<service>/toolsets/<toolset>/provider.go`

That generated provider implements a dispatcher that decodes the tool payload JSON using generated codecs, adapts into the bound method payload (via generated transforms), calls the bound method, and re-encodes the tool result JSON together with any declared server-data (optional observer-facing server-data and always-on server-only metadata).

To run it, wire the generated provider into the runtime provider loop. The
callback names below stand for direct adapters to the corresponding generated
registry operations:

```go
handler := toolsetpkg.NewProvider(serviceImpl)
providerID := mustRequiredEnv("HOSTNAME") + "/" + toolsetID
admissionRevision := mustRequiredEnv("TOOL_REGISTRY_ADMISSION_REVISION")
go func() {
    err := toolprovider.Serve(
        ctx,
        pulseClient,
        toolsetID,
        handler,
        toolprovider.Registration{
            AdmissionRevision:  admissionRevision,
            Register:           registerProvider,
            Drain:              drainProvider,
            Release:            releaseProvider,
            Claim:              claimToolCall,
            Complete:           completeToolCall,
            PublishOutputDelta: publishToolOutputDelta,
            ReportOverload:     reportToolCallOverload,
        },
        toolprovider.Options{ProviderID: providerID, Pong: pongProvider},
    )
    if err != nil && !errors.Is(err, context.Canceled) {
        panic(err)
    }
}()
```

`Serve` creates one incarnation, registers it before opening shared
consumption, renews its lease, and performs drain/settle/release shutdown. Use
one admission revision for same-contract replicas and rolling updates. Change
it to create a new execution fence. Provider registration and consumer
CallTool/RetryTool requests must declare `toolregistry.WireProtocolVersion`.

The registration token is the immutable admission-generation fence. The exact
token follows the request through provider claim, output deltas, overload
control, completion, and consumer result filtering. Stale queued work is
settled as `stale_registration`; it is never executed under a replacement
provider generation.

This integration has separate ownership:

- **Registry gateway**: owns provider generations, exact-CAS membership,
  durable call decisions, deadlines, result retention, and canonical results.
- **Service provider loop**: claims admitted work and executes generated
  provider adapters under the registry-issued deadline.

### Registry-Routed Execution (Agent/Consumer Side)

On the consumer side (an agent calling registry-routed toolsets), the runtime needs a `ToolCallExecutor` that:

- calls the registry gateway with stable run and model tool-call identity to
  obtain the durable admitted or rejected decision, then
- reads the retained per-call result stream independently and decodes the exact
  token-matched result using compiled tool specs/codecs.

Loom MCP provides a reusable executor implementation in `runtime/toolregistry/executor` that implements `runtime.ToolCallExecutor`:

```go
import (
    toolregexec "github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry/executor"
)

exec := toolregexec.New(registryClient, pulseClient, specs)

// Use exec.Execute as the executor for registry-backed toolsets.
```

The registry wire protocol and deterministic stream IDs are defined in `runtime/toolregistry`:

- Toolset request stream: `toolset:<toolsetID>:requests`
- Per-call result stream: `result:<toolUseID>`

Identical retries observe the same decision and result stream. An admitted call
that later loses routing certainty resolves as `outcome_unknown`; callers must
not reclassify it as safely replannable. Queue publication rejects atomically
when unread backlog is full, and stream trimming is garbage collection only.
The registry owns one absolute execution deadline and one later absolute result
retention deadline for every call.

### Registry discovery & catalog sync (runtime/registry)

If you need runtime discovery of toolsets and schemas (e.g., tool catalogs that change without a `loom gen`),
use the client-side components in `runtime/registry`:

- `GRPCClientAdapter`: wraps a generated gRPC registry client into a `RegistryClient` interface
- `Manager`: multi-registry discovery with caching and periodic sync (`StartSync`/`StopSync`)
- `SearchClient`: cross-registry search with semantic-first + keyword fallback when supported

Both plain `Manager.Search` and the richer `SearchClient.Search` use the same
concurrent fan-out and partial-failure contract: every selected registry starts
independently, successful results are retained when only some registries fail,
and the call fails only when every selected registry fails. `SearchClient`
adds semantic fallback, filtering, relevance ordering, and result limits after
that shared collection step.

These are client-side helpers. The standalone registry service implementation lives under `loom-mcp/registry`.
Generated registry-backed agent registrations intentionally freeze their discovered tool specs at
`RegisterUsedToolsets`: call the generated `DiscoverAndPopulate` during startup, inspect through
`Specs()`, then register. Refresh attempts after registration fail so runtime schemas and codecs
cannot drift while workflows are executing; restart and re-register the agent to adopt a newer catalog.

**Inline tools** — Custom executor implementation:

```go
reg := runtime.ToolsetRegistration{
    Name: "myservice.helpers",
    Execute: func(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
        // Decode payload, execute logic, return the durable result envelope.
        return runtime.Executed(&planner.ToolResult{
            Name:       call.Name,
            ToolCallID: call.ToolCallID,
            Result:     /* ... */,
        }), nil
    },
    Specs: []tools.ToolSpec{...},
}
rt.RegisterToolset(reg)
```

### Runtime Interceptors

`runtime.WithInterceptors(...)` registers inline call-path interceptors. Hooks
publish durable observability events; interceptors run before or after execution
and may change requests, results, or event publication before durable events are
emitted.

Interceptors are opt-in typed interfaces. A value may implement one or more of:

- `RunInterceptor`: `BeforeRun` / `AfterRun`
- `ToolInterceptor`: `BeforeTool` / `AfterTool`
- `ModelInterceptor`: `BeforeModel` / `AfterModel`
- `EventInterceptor`: `BeforeEvent` / `AfterEvent`

Ordering is registration order. Runtime-level interceptors run before agent
interceptors. Generated `RunPolicy(func(){ Interceptors("audit") })` stores
design-owned IDs; application code supplies the implementations with
`runtime.WithNamedInterceptors(...)`.

Key mutation rules:

- `BeforeTool` can rewrite raw JSON payloads or return a `ToolExecutionResult`
  to skip the executor. `AfterTool` can replace the `planner.ToolResult`, the
  full `ToolExecutionResult`, or the error.
- `BeforeModel` wraps clients returned by `PlannerContext.ModelClient(id)` after
  cache/tool-policy decorators and before tracing. It can rewrite the
  `model.Request`. For non-streaming completions it can also short-circuit with
  a response. Streaming calls do not synthesize a `model.Streamer` from a
  response short-circuit; returning a response from `BeforeModel` on `Stream`
  fails the call. `AfterModel` can replace the response or error.
- `BeforeEvent` runs in `runtime.publish_hook` before runlog append, stream
  publish, and hook-bus publish. Returning `Drop` makes the event absent from all
  three surfaces.
- Empty `After*` decisions are observer no-ops. `AfterTool`, `AfterRun`, and
  `AfterModel` adopt the decision's `Err` (including a nil `Err`, which clears
  the current error) only when the decision also replaces the result
  (`Execution`/`Result`, `Output`, or `Response`) or carries a non-nil `Err`.
  `AfterEvent` adopts only a non-nil `Err`; observer interceptors can never
  clear an event publication error, so a failed canonical run-log append still
  fails the run.
- Interceptor errors short-circuit the active path.

`runtime.NewRetryAndReflectInterceptor(...)` implements the tool path to convert
tool execution errors into planner-visible tool errors with structured
`planner.RetryHint` guidance, keeping the run alive so the planner can repair
the call. Its generated error and retry message use fixed framework text. They
do not persist the raw service error because that error can contain submitted
arguments or secrets.

### Model-Facing Skills

`runtime.NewSkillToolsetRegistration(...)` exposes local skill directories as
ordinary model tools:

- `<toolset>.list_skills`
- `<toolset>.load_skill`
- `<toolset>.load_skill_resource`

This complements MCP `skill://` resources. MCP clients can discover and read
skills through resources, while model planners can advertise the same skill
roots as tool calls when the model needs to inspect skill instructions.

Skill metadata comes from `SKILL.md` YAML frontmatter:

```yaml
id: code-review
name: Code Review
description: Review code changes for correctness.
allowed_tools: [shell]
preload: on_start
reload: per_call
```

`list_skills` returns this metadata. `load_skill` and `load_skill_resource`
include metadata on the returned content. `preload: on_start` caches `SKILL.md`
when the toolset registration is built; `reload: per_call` reloads files from
disk for each load request. Duplicate skill IDs and invalid mode values fail
discovery.

### Artifact Store And Tools

Configure persisted run artifacts with `runtime.WithArtifactStore(...)`. Tools may attach
`artifact.Content` values to `planner.ToolResult.Artifacts`; the runtime saves those bodies
and propagates only `artifact.Ref` values across workflow-safe boundaries:

- `planner.ToolOutput.Artifacts`
- `api.ToolEvent.Artifacts` and `api.ToolCallOutput.Artifacts`
- `hooks.ToolResultReceivedEvent.Artifacts`
- durable memory `ToolResultData.Artifacts`

`runtime.NewArtifactToolsetRegistration(...)` exposes two model-facing tools:

- `<toolset>.list_artifacts` returns refs filtered by `mime_type`, metadata, and limit.
- `<toolset>.load_artifact` returns bounded `{content, mime_type, truncated, size_bytes}`.

Artifact bodies are never embedded in hook/runlog payloads or planner workflow envelopes.

#### Executor result envelope: `runtime.ToolExecutionResult`

Tool executors return `*runtime.ToolExecutionResult` rather than `*planner.ToolResult` directly:

```go
type ToolExecutionResult struct {
    ToolResult *planner.ToolResult // Durable planner-visible result (always required)
    Pause      *api.ToolPause      // Optional runtime-owned pause signal for the current batch
}
```

- `ToolResult` is the durable, planner-facing tool outcome. It lands in the
  transcript, the cumulative `ToolOutputs` history, and the
  `ToolResultReceivedEvent` hook fanout exactly as before.
- `Pause` is a current-batch-only signal. When non-nil it is projected into the
  workflow's await queue (e.g. as an `AwaitClarification` item) and never
  persists into cumulative `ToolOutputs`, so replaying a pause envelope cannot
  re-trigger an await on a later turn.

For the common case where an executor just emits a durable result with no
runtime-owned pause, use the `runtime.Executed(...)` helper:

```go
return runtime.Executed(&planner.ToolResult{ /* ... */ }), nil
```

To request a clarification from the user from inside an executor, attach a
`ToolPause`:

```go
return &runtime.ToolExecutionResult{
    ToolResult: &planner.ToolResult{
        Name:       call.Name,
        ToolCallID: call.ToolCallID,
        Result:     partialResult,
    },
    Pause: &api.ToolPause{
        Clarification: &api.ToolPauseClarification{
            ID:       "ambiguous-target",
            Question: "Which target did you mean: A or B?",
        },
    },
}, nil
```

The runtime contract enforces that a `Pause` must not be paired with a tool
error and must carry a non-nil payload (validated by
`validateToolPauseContract`).

> **Breaking change (v0.52.0)**: the executor signature changed from
> `func(ctx, *ToolRequest) (*planner.ToolResult, error)` to
> `func(ctx, *ToolRequest) (*ToolExecutionResult, error)`. Custom executors,
> the registry-routed executor, the MCP executor, and example scaffolds all
> need to wrap their return value via `runtime.Executed(...)` or construct
> the envelope explicitly.

**Agent-as-tool** — Nested agent execution:

```go
reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{
    AgentID: agent.Ident("service.nested"),
    Route:   runtime.AgentRoute{...},
    // Optional per-tool prompts/templates
})
```

### ToolCallMeta

Executors receive explicit per-call metadata:

```go
type ToolCallMeta struct {
    RunID            string  // Workflow execution identifier
    SessionID        string  // Logical session grouping
    TurnID           string  // Conversational turn identifier
    ToolCallID       string  // Unique tool invocation ID
    ParentToolCallID string  // Parent tool call (for agent-as-tool)
}
```

### Optional server-data (reserved `"server_data"` payload field)

Tools can optionally produce **observer-facing server-data** (often projected into UI artifacts) that is never sent to model providers.
The runtime supports a per-call optional server-data toggle via a reserved top-level tool payload field:

- `{"server_data":"auto"}` — use the tool default
- `{"server_data":"on"}` — enable optional server-data (when the tool declares it)
- `{"server_data":"off"}` — disable optional server-data for this call

The runtime strips the reserved `"server_data"` field from the execution payload before decoding, and records the
normalized value on the tool call metadata (`ServerDataMode`). Tool payload schemas must not define a top-level
property named `"server_data"`.

### Bounded Results

Tools that return partial views of larger datasets should use the `BoundedResult`
DSL helper. This enforces a canonical bounded-result contract:
bounded tools declare their contract in `tools.ToolSpec.Bounds`, successful
executions must populate `planner.ToolResult.Bounds`, and the runtime projects
the canonical bounds fields (`returned`, `total`, `truncated`,
`refinement_hint`, and optional `next_cursor`) into the emitted result JSON and
hook/stream payloads.

The runtime enforces one strict contract across all result ingress paths
(regular execution and externally provided await results):

- unbounded tools must not return bounds metadata,
- error tool results must not return bounds metadata,
- successful bounded results must include bounds metadata,
- when `truncated=true`, bounds must include either `next_cursor` or
  `refinement_hint`.

```go
type Bounds struct {
    Returned       int     // Items in this response
    Total          *int    // Total items available (when known)
    Truncated      bool    // True if limits were applied
    RefinementHint string  // Guidance for narrowing queries
}
```

The runtime surfaces bounds via `ToolResult.Bounds`, encoded `tool_result` JSON,
result-hint templates under `.Bounds`, hook events, and stream events. Services
own truncation logic; the runtime only propagates and projects what tools
report.

Transcript-facing tool results use a stricter provider contract than execution
boundaries:

- canonical raw bytes live in `ToolOutput.Result`, `ToolResultReceivedEvent.ResultJSON`,
  and durable memory-event `result_json`,
- `model.ToolResultPart.Content` carries semantic provider-facing content only:
  decoded JSON-compatible values on success or plain error text with `IsError=true`,
- oversized successful transcript content projects to an explicit omission object:
  `{"omitted":true,"reason":"size_limit","preview":"...","bounds":{...}}`.

For method-backed `BindTo` tools, the bound service method result still needs to
carry the canonical bounded fields so the generated executor can build
`planner.ToolResult.Bounds` before runtime projection. Explicit tool-facing
`Return(...)` shapes must not duplicate those canonical fields. Within the bound
method result, only `returned` and `truncated` may be required; `total`,
`refinement_hint`, and `next_cursor` remain optional and are omitted from emitted
JSON whenever runtime bounds omit them. `BoundedResult(...)` still owns the
tool-facing contract exposed to models.

When a service boundary must assemble canonical result JSON outside
`ExecuteToolActivity` itself, use `runtime.EncodeCanonicalToolResult(...)`
instead of calling the generated result codec and bounded-result projection
helpers separately.

---

## Agent-as-Tool Composition

Agents can expose tools via `Export` blocks and consume them via `Use`. When invoked,
nested agents execute as child workflows with their own run IDs and event streams.
Temporal child workflows use request-cancel parent-close behavior so canceling
or closing a parent requests child cancellation instead of terminating the
child as a generic failure.

### How It Works

1. Parent planner requests tool (e.g., `"service.analysis.analyze"`)
2. Runtime identifies it as an agent-tool via `ToolSpec.IsAgentTool`
3. Runtime starts child workflow using `AgentToolConfig.Route`
4. Child agent executes its own plan/execute loop
5. Runtime returns a parent `ToolResult` derived from the child run output (final text and/or finalizer output, plus aggregated telemetry). **Artifacts are not propagated to the parent tool result**; they remain attached to the child tool events.
6. `ChildRunLinked` event links parent and child for streaming

### Configuration

```go
reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{
    AgentID:         agent.Ident("service.data-analyst"),
    Route:           runtime.AgentRoute{
        ID:               agent.Ident("service.data-analyst"),
        WorkflowName:     "DataAnalystWorkflow",
        DefaultTaskQueue: "orchestrator.data-analyst",
    },
    SystemPrompt:    "You are a data analysis expert.",
    AgentToolContent: runtime.AgentToolContent{
        Templates: compiledTemplates, // Per-tool user message templates (optional)
        Texts:     textMessages,      // Alternative to templates (optional)
    },
    JSONOnly:        true,                // Return structured results
    Finalizer:       myFinalizer,         // Custom result aggregation
})
```

### Per-Tool Content

Configure how tool payloads become the nested agent's initial user message.
When you do not configure consumer-side content, the runtime uses a deterministic
default: the canonical JSON tool payload bytes (verbatim) as the nested user
message.

```go
// Plain text for all tools
runtime.WithTextAll(toolIDs, "Process this: {{ . }}")

// Template for specific tool
runtime.WithTemplate(toolID, compiledTemplate)

// PromptSpec for a tool (optional; payload-only)
runtime.WithPromptSpec(toolID, "my.prompt.id")

// Custom prompt builder
cfg.Prompt = func(id tools.Ident, payload any) string {
    return fmt.Sprintf("Handle %s request: %v", id.Tool(), payload)
}
```

### Finalizers

Finalizers aggregate child results into the parent tool result:

```go
// Pass-through: use JSONOnly aggregation
runtime.PassThroughFinalizer()

// Tool-based: call a dedicated aggregation tool
runtime.ToolResultFinalizer(tools.Ident("helpers.aggregate"), func(ctx, input) (any, error) {
    return map[string]any{"children": input.Children}, nil
})

// Custom: full control over aggregation
runtime.FinalizerFunc(func(ctx, input FinalizerInput) (ToolResult, error) {
    // Build result from input.Children
    return planner.ToolResult{Result: aggregated}, nil
})
```

---

## Human-in-the-Loop

Runs can pause and resume via interrupt signals, enabling approval workflows,
clarification requests, and external tool integration.

### Pause and Resume

```go
// Pause a run (from outside the workflow)
err := rt.PauseRun(ctx, interrupt.PauseRequest{
    RunID:       "run-123",
    Reason:      "human_review",
    RequestedBy: "policy-engine",
})

// Resume after approval
err := rt.ResumeRun(ctx, interrupt.ResumeRequest{
    RunID:       "run-123",
    Notes:       "Approved by admin",
    Messages:    additionalMessages, // Optional
})
```

### Clarification Requests

Planners can request missing information:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        Clarification: &planner.AwaitClarification{
            ID:            "clarify-device",
            Question:      "Which device should I configure?",
            MissingFields: []string{"device_id"},
        },
    },
}
```

The runtime pauses the workflow and emits an `AwaitClarification` event. Callers
respond via:

```go
err := rt.ProvideClarification(ctx, interrupt.ClarificationAnswer{
    RunID:  "run-123",
    ID:     "clarify-device",
    Answer: "Device ID is ABC-123",
})
```

### External Tools

Planners can request tools that execute out-of-band:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        ExternalTools: &planner.AwaitExternalTools{
            ID: "external-1",
            Items: []planner.AwaitToolItem{{
                Name:       tools.Ident("external.fetch"),
                ToolCallID: "tc-ext-1",
                Payload:    jsontext.Value(`{"url":"..."}`),
            }},
        },
    },
}
```

Callers provide results via:

```go
err := rt.ProvideToolResults(ctx, &api.ToolResultsSet{
    RunID: "run-123",
    ID:    "external-1",
    Results: []*api.ProvidedToolResult{
        {
            ToolCallID: "toolcall-1",
            Name:       tools.Ident("chat.ask_question.ask_question"),
            // Contract: canonical JSON bytes matching the tool's Return schema.
            Result: jsontext.Value(`{"answers":[{"question_id":"...","selected_ids":["approve"]}]}`),
        },
    },
})
```

Provided tool results are strict boundary inputs:

- each item must be exactly one of: `Error` or non-null `Result`,
- if the tool is bounded and successful, `Bounds` must be present and satisfy
  bounded-result invariants.

Those rules apply only at execution/history boundaries. Once the runtime projects
tool output into transcript messages, models never see raw `Result` bytes or
structured Go error values.

### Typed Human Input

Graph workflow planners can request schema-typed human input without pretending
the answer is a tool result:

```go
return &planner.PlanResult{
    Await: &planner.Await{
        Items: []planner.AwaitItem{planner.AwaitTypedInputItem(&planner.AwaitTypedInput{
            ID:     "approval",
            Title:  "Approval",
            Schema: jsontext.Value(`{"type":"object","properties":{"approved":{"type":"boolean"}}}`),
        })},
    },
}
```

The runtime emits an `AwaitTypedInput` hook event, which the stream subscriber
forwards to clients as an `await_typed_input` stream event (`AwaitTypedInputPayload`
with `id`, `title`, and `schema`) alongside a `workflow` event with
`phase="paused"`, so UIs can render an input form instead of showing the run as
hung. Callers resume with:

```go
err := rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{
    RunID:   "run-123",
    ID:      "approval",
    Payload: jsontext.Value(`{"approved":true}`),
})
```

The resumed planner receives the answer in `PlanResumeInput.TypedInputs`.
Typed-input answers are not copied into `ToolOutputs`.

### Tool Confirmation (Design-Time + Runtime Overrides)

Loom MCP supports **runtime-enforced** confirmation gates for sensitive tools.

There are two ways to enable confirmation:

- **Design-time (recommended, common case):** declare `Confirmation(...)` inside a tool DSL.
  Codegen stores the confirmation policy in the generated `tools.ToolSpec.Confirmation`.
- **Runtime (dynamic/override):** supply `runtime.WithToolConfirmation(...)` when constructing the
  runtime. This can require confirmation for additional tools and/or override the design-time behavior
  for specific tool IDs.

At execution time, the workflow:

- Emits an out-of-band confirmation request (using `AwaitConfirmation`) before executing the
  target tool call.
- Waits for a user approval/denial decision.
- Executes the tool only when approved.
- When denied, synthesizes a **schema-compliant** tool result (so the transcript remains valid and
  the planner can react to the denial deterministically).

#### Confirmation protocol

The runtime uses a runtime-owned confirmation protocol to obtain an explicit approval/denial
decision before executing a confirmed tool.

- **Await payload** (hook + stream event):

  ```json
  {
    "id": "...",
    "title": "...",
    "prompt": "...",
    "tool_name": "atlas.commands.change_setpoint",
    "tool_call_id": "toolcall-1",
    "payload": { "...": "canonical tool arguments (JSON)" }
  }
  ```

- **Provide decision** (using `ProvideConfirmation`):

  ```go
  err := rt.ProvideConfirmation(ctx, interrupt.ConfirmationDecision{
      RunID:       "run-123",
      ID:         "await-1",
      Approved:    true,              // or false
      RequestedBy: "user:123",        // optional, for audit
      Labels:      map[string]string{"source": "front-ui"},
      Metadata:    map[string]any{"ticket_id": "INC-42"},
  })
  ```

Consumers should treat confirmation as a **runtime protocol**, not as a user-defined tool:

- Use the accompanying `RunPaused` reason (`await_confirmation`) to decide when to display a confirmation UI.
- Do not couple UI behavior to a specific confirmation tool name; treat it as an internal transport detail.

This keeps the runtime generic: any UI/system can implement a compatible confirmation transport.

### Tool authorization events

When a decision is provided via `ProvideConfirmation`, the runtime emits a first-class authorization event:

- **Hook event**: `hooks.ToolAuthorization`
- **Stream event type**: `tool_authorization`

This event is emitted exactly once per confirmed tool call and captures the durable authorization record:

- `tool_name`: the tool being authorized
- `tool_call_id`: the tool call identifier
- `approved`: true/false decision
- `summary`: deterministic runtime-rendered summary (derived from the confirmation prompt)
- `approved_by`: copied from `interrupt.ConfirmationDecision.RequestedBy` and intended to be a stable principal identifier (for example, `user:<id>`)

The event is emitted immediately after the decision is received:

- **Approved**: emitted before the tool executes.
- **Denied**: emitted before the denied tool result is synthesized.

Consumers (UIs, audit stores, session recorders) should rely on `tool_authorization` for “who/when/what” rather than inferring authorization from tool results.

#### Runtime validation

The runtime treats confirmation as a boundary and validates:

- The confirmation `ID` matches the pending await identifier when provided.
- The decision object is well-formed (non-empty `RunID`, boolean `Approved` value).

Notes:

- Confirmation templates (`PromptTemplate` and `DeniedResultTemplate`) are Go `text/template` strings
  executed with `missingkey=error`. In addition to the standard template functions (e.g. `printf`),
  Loom MCP provides:
  - `json v` → JSON encodes `v` (useful for optional pointer fields or embedding structured values).
  - `quote s` → returns a Go-escaped quoted string (like `fmt.Sprintf("%q", s)`).

---

## Hooks and Streaming

### Hook Bus

The runtime publishes events to an internal bus (`hooks.Bus`). Default subscribers
handle memory persistence and stream forwarding.

**Determinism note:** When using a durable workflow engine (e.g., Temporal),
workflow code must be deterministic and must not trigger external I/O. The
runtime therefore routes workflow-emitted hook events through a dedicated hook
activity (`runtime.publish_hook`), which publishes to the bus outside the
workflow thread. Activities and other non-workflow code publish directly.

Hook activity payloads use JSON v2 with the standard nanosecond compatibility
format for `time.Duration`. Per-run policy budgets and timeouts therefore cross
the workflow/activity boundary as integer nanoseconds, while JSON field tags and
duration-valued map keys retain their normal encoding behavior.

**Event types:**

| Event                                       | When                                                |
| ------------------------------------------- | --------------------------------------------------- |
| `RunStarted`                                | Run begins                                          |
| `RunCompleted`                              | Run finishes (success, failed, canceled)            |
| `RunPaused` / `RunResumed`                  | Human-in-the-loop and await transitions              |
| `RunPhaseChanged`                           | Phase transitions (planning, executing_tools, etc.) |
| `PromptRendered`                            | Runtime resolves and renders a prompt spec          |
| `ToolCallScheduled`                         | Tool activity scheduled                             |
| `ToolResultReceived`                        | Tool completes                                      |
| `ToolCallUpdated`                           | Parent tool discovers more children                 |
| `ToolCallArgsDelta`                         | Best-effort streamed argument delta                 |
| `AssistantMessage` / `AssistantTurnCommitted` | Assistant output and committed-turn boundary      |
| `PlannerNote` / `ThinkingBlock`             | Planner reasoning                                   |
| `AwaitClarification` / `AwaitQuestions`     | Clarification pause requests                        |
| `AwaitTypedInput` / `AwaitExternalTools`    | Typed or externally executed work pause requests    |
| `AwaitConfirmation` / `ToolAuthorization`   | Approval request and recorded decision              |
| `RetryHintIssued` / `MemoryAppended`        | Derived retry and transcript projection events      |
| `PolicyDecision`                            | Policy evaluation result                            |
| `Usage`                                     | Token usage report                                  |
| `HardProtectionTriggered`                   | Runtime hard-protection circuit activation          |
| `ChildRunLinked`                            | Agent-as-tool child run link                        |

### Custom Subscribers

```go
sub := hooks.SubscriberFunc(func(ctx context.Context, evt hooks.Event) error {
    switch e := evt.(type) {
    case *hooks.ToolResultReceivedEvent:
        log.Printf("Tool %s completed in %v", e.ToolName, e.Duration)
    }
    return nil
})

subscription, _ := rt.Bus.Register(sub)
defer subscription.Close()
```

### Stream Sink

The `stream.Sink` interface delivers client-facing events:

```go
type Sink interface {
    Send(ctx context.Context, event Event) error
    Close(ctx context.Context) error
}
```

**Stream event types:**

| Event                  | Payload                                                                      |
| ---------------------- | ---------------------------------------------------------------------------- |
| `prompt_rendered`      | `PromptRenderedPayload` (`prompt_id`, `version`, `scope`)                    |
| `tool_start`           | `ToolStartPayload` (tool_call_id, tool_name, payload)                        |
| `tool_end`             | `ToolEndPayload` (result, error, duration, telemetry)                        |
| `tool_update`          | `ToolUpdatePayload` (expected_children_total)                                |
| `tool_call_args_delta` | Best-effort planner-authored argument progress; runtime model fragments stay private |
| `tool_output_delta`    | Incremental tool output                                                        |
| `assistant_reply`      | `AssistantReplyPayload` (`text`, optional `presentation_id`)                 |
| `assistant_turn`       | Atomic committed assistant content (`message` legacy, or `presentation_ids` plus ordered `messages`) |
| `planner_thought`      | `PlannerThoughtPayload` (note/thinking fields, optional `presentation_id`)   |
| `model_presentation`   | `ModelPresentationPayload` (`presentation_id`, `started`/`accepted`/`discarded`) |
| `await_clarification`  | `AwaitClarificationPayload`                                                  |
| `await_confirmation`   | `AwaitConfirmationPayload`                                                   |
| `await_questions`      | `AwaitQuestionsPayload` (`tool_name`, `tool_call_id`, `payload`, `questions`) |
| `await_typed_input`    | `AwaitTypedInputPayload` (`id`, `title`, `schema`)                           |
| `await_external_tools` | `AwaitExternalToolsPayload`                                                  |
| `tool_authorization`   | Recorded approval or denial for a tool call                                   |
| `usage`                | `UsagePayload` (input_tokens, output_tokens)                                 |
| `workflow`             | `WorkflowPayload` (phase, status, error_kind, retryable, error, debug_error) |
| `child_run_linked`     | `ChildRunLinkedPayload` (child run link)                                     |
| `session_stream_started` / `session_stream_end` | Session stream lifecycle                                  |
| `run_stream_end`       | Run stream lifecycle boundary                                                 |

### Stream Profiles

Control which events reach each audience:

```go
// All events, child runs linked
stream.DefaultProfile()

// User chat view (default for most UIs)
stream.UserChatProfile()

// Debug view (all events; child runs linked)
stream.AgentDebugProfile()

// Metrics only (usage, workflow)
stream.MetricsProfile()
```

### Workflow payload contract (phases, terminal status, and errors)

The runtime emits:

- `RunPhaseChanged` hook events for **non-terminal** phase transitions (`planning`, `executing_tools`, `synthesizing`, etc.)
- `RunPaused` hook events when execution is suspended awaiting external action (awaits, manual pause)
- a single `RunCompleted` hook event per run for the **terminal** lifecycle state

The stream subscriber translates these into `workflow` stream events:

- **Non-terminal updates** (from `RunPhaseChanged`): `phase` only.
- **Pause updates** (from `RunPaused`): `phase="paused"` plus `reason` (for example, `await_queue` or `await_clarification`). The stream does not end; the run resumes when the awaited input arrives.
- **Terminal update** (from `RunCompleted`): `status` + terminal `phase`.

Terminal status mapping:

- `status="success"` → `phase="completed"`
- `status="failed"` → `phase="failed"`
- `status="canceled"` → `phase="canceled"`

Cancellation is not an error:

- For `status="canceled"`, the workflow payload must not include a user-facing `error`.
- Every leaf in the error graph must be `context.Canceled` or a Temporal
  cancellation. The runtime treats mixed cancellation and failure graphs as
  failed runs and preserves each error leaf.
- Temporal records a mixed graph as a non-retryable application failure. The
  adapter restores its cancellation evidence after failure conversion. This
  applies to activities, children, top-level waits, and restart queries.
- `context.DeadlineExceeded` is a timeout failure. It is not a cancellation.
- A canceled tool activity or agent child stops the owning run. The runtime
  does not convert this condition into a failed tool result.
- The classifier makes each decision with one bounded traversal. It detects
  cycles. Invalid or oversized error graphs fail the run.
- Temporal replaces an invalid graph with a static, non-retryable failure. Its
  failure converter does not traverse the rejected graph.
- Workflow, one-shot, observed, and restart completion paths replace invalid
  graphs before interceptors, hook classification, logging, or serialization.

Failures are structured:

- For `status="failed"`, the workflow payload includes:
  - `error_kind`: stable classifier (provider kinds like `rate_limited`, `unavailable`, or runtime kinds like `timeout`/`internal`)
  - `retryable`: whether retrying may succeed without changing input
  - `error`: **user-safe** message suitable for direct display
  - `debug_error`: raw error string for logs/diagnostics (not for UI)

### Event reliability and authority

Runtime event surfaces are related projections, not interchangeable durable
logs:

| Surface | Role | Failure behavior |
| --- | --- | --- |
| Workflow ledger | Live deterministic planner state for the active run | Owned by the workflow engine; not a general event subscription API |
| `runlog.Store` | Canonical append-only hook event record | Append or run-metadata update failure fails the hook activity so the engine can retry or stop the run |
| Session stream sink | Active-session client projection | Best effort; lookup/send failures are logged, ended sessions are skipped, and sessionless one-shot runs do not stream |
| Hook bus | Local subscriber and derived-projection fanout | Built-in subscribers are best effort; explicitly critical subscribers may propagate an error and fail the hook activity |
| `memory.Store` | Derived per-run transcript/event projection | The built-in memory subscriber is best effort and may be incomplete after a subscriber failure |
| `memory.Service` | Explicit long-term entry store | Written only through its `PutEntry`/ingest contract; it is not the transcript or run log |

Session stores also persist the coarse run lifecycle. The first committed
`completed`, `failed`, or `canceled` status is terminal. Later updates may add
metadata or prompt references, but they cannot replace that terminal outcome or
reopen the run as pending, running, or paused. This makes retrying concurrent
terminal hook projections safe across in-memory and Mongo-backed stores.

`ToolCallArgsDelta` is intentionally excluded from the durable run event log and
hook bus because it is a high-volume, best-effort UX signal. The finalized tool
call remains canonical. `transcript.BuildMessagesFromEvents` consumes
`memory.Event` values; it is not a direct runlog replay API. Applications that
need a durable projector must install a critical subscriber or build from a
separately defined durable contract rather than assuming stream or default
memory delivery is complete.

## Policy Enforcement

Policy engines decide which tools are available each turn and enforce caps.

### Policy Engine Interface

```go
type Engine interface {
    Decide(ctx context.Context, input Input) (Decision, error)
}
```

**Input:**

```go
type Input struct {
    RunContext    run.Context        // Run identifiers and labels
    Tools         []ToolMetadata     // Candidate tools
    RetryHint     *RetryHint         // Planner guidance after failures
    RemainingCaps CapsState          // Current execution budgets
    Requested     []tools.Ident      // Explicitly requested tools
    Labels        map[string]string  // Context labels
}
```

**Decision:**

```go
type Decision struct {
    AllowedTools []tools.Ident      // Tools permitted this turn
    Caps         CapsState          // Updated execution budgets
    DisableTools bool               // Force final response
    Labels       map[string]string  // Labels to propagate
    Metadata     map[string]any     // Audit trail data
}
```

### Caps State

```go
type CapsState struct {
    MaxToolCalls           int
    RemainingToolCalls     int
    MaxRecoveryTurns       int
    RemainingRecoveryTurns int
    ExpiresAt              time.Time // Deprecated; ignored by runtime
}
```

The default `MaxRecoveryTurns` value is 3. The runtime uses this default when
the design and the run do not set a positive value.

One recovery turn runs one replacement planner activity after rejected model
output or recoverable tool output. Successful registered domain work resets
the allowance. Failed work and `runtime.tool_unavailable` do not reset it.

Model recovery stores a fingerprint, byte count, usage, attempt, and bounded
framework guidance. It does not store rejected model output or submitted tool
arguments. The activity records each rejected unary call, including calls that
the planner catches. It matches recovery to the exact error that the planner
returns. Concurrent model calls cannot erase that match. Token totals include
all accepted and rejected billed calls. Every durable total uses checked
addition and fails if a count overflows. Generated retry hints are at most 4096
encoded bytes. For a stream, the runtime publishes provisional text and thinking
live but stages their canonical commitment until terminal validation and
provider cleanup both succeed. A rejected or cleanup-failed stream cannot append
its presentation content to the canonical run log.

Structured-output and output-limit recovery disables all tools for the
replacement turn. The runtime checks the same effective tool catalog at the
model boundary and the workflow boundary. A tool-call replacement must carry a
non-nil, bounded, unique copy of the rejected request catalog. The workflow
rejects a catalog that adds a tool outside the active policy. When the recovery
allowance is empty, a normal start or resume enters the tool-free failure
finalizer. A rejection from that finalizer ends the run and does not recurse.
The model and recovery contracts accept at most 256 tool definitions per
request, so every accepted request has a durable exact-catalog representation.

### Bookkeeping budgets

Generated policy metadata classifies each tool as `budgeted` or `bookkeeping`.
`MaxToolCalls` counts only budgeted domain calls. The per-turn limit counts all
calls.

The runtime accepts a provider tool-call batch only when the complete batch
fits. It never removes selected calls to make a batch fit the remaining budget.
A bookkeeping-only batch can run when the domain-call budget is empty.

Successful bookkeeping calls do not reset the recovery budget. Their requests
and results remain in the durable transcript. Their successful outputs do not
enter the next planner request.

`TerminalRun` tools must use the bookkeeping class. A successful terminal call
completes the run without another planner request. A failed terminal call fails
the run.


Caps constrain the calls a planner selected; they do not truncate the tool
catalog shown to the planner. The pre-plan policy envelope therefore carries
the full policy allowlist, and per-turn or remaining-call caps are applied only
after planning. The runtime-owned `tool_unavailable` recovery call remains
executable under an active allowlist so rewritten unavailable calls can produce
their structured recovery result. Per-run tool and tag filters also preserve
this internal recovery call. The internal tool is not a policy candidate. The
runtime replaces every direct call payload with the exact server-owned catalog
at the model boundary. The activity boundary preserves that narrower catalog
when the run policy allows more tools.

`CapsState` owns counter budgets only. Its legacy `ExpiresAt` field is retained
for source compatibility but is not merged or enforced. Absolute wall-clock
expiry would create a second deadline authority and would not preserve the
runtime's paused-wait semantics. Configure active-work time through
`RunPolicy.TimeBudget` or `runtime.WithRunTimeBudget(...)`; enforce an independent
end-to-end wall-clock SLA at the caller or workflow boundary.

### Per-Run Policy Overrides

Callers can override policy for specific runs:

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunMaxToolCalls(5),
    runtime.WithRunTimeBudget(2*time.Minute),
    runtime.WithRestrictToTool(tools.Ident("helpers.search")),
    runtime.WithAllowedTags([]string{"safe", "read-only"}),
    runtime.WithDeniedTags([]string{"destructive"}),
)
```

These overrides are included in the `RunStarted` hook payload. Duration-bearing
fields use integer nanoseconds on that durable boundary.

### Terminal run policies

`WithRunCompletionTool` requires one budgeted tool to succeed before the run
can complete. The first successful call returns its result and skips another
planner request.

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunCompletionTool(tools.Ident("reports.persist")),
)
```

The completion tool must belong to the agent. It cannot be a bookkeeping,
terminal, or confirmation tool. A completion-tool run cannot use whole-workflow
retries.

The planner must select the completion tool as the only action in its response.
Final responses and delegated completion calls fail the run. Limit exhaustion
also fails the run if the completion tool did not succeed.

`WithLimitTerminalPlans` supplies fixed terminal calls for three limit reasons:

- the active time budget
- the domain tool-call budget
- the recovery-turn budget

```go
plans := runtime.LimitTerminalPlans{
    TimeBudget: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"time_budget"}`),
    },
    ToolCallCap: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"tool_call_cap"}`),
    },
    RecoveryCap: runtime.LimitTerminalCall{
        Name: "reports.limit",
        Payload: rawjson.Message(`{"reason":"recovery_cap"}`),
    },
}

client.Run(ctx, "session-1", msgs,
    runtime.WithLimitTerminalPlans(plans),
)
```

All three calls are required. Each call must target an agent-owned terminal
bookkeeping tool without confirmation. The runtime validates each payload before
the first planner request.

A fixed limit plan skips the finalizer model call. The runtime assigns a
deterministic tool-call ID before execution. A failed terminal side effect fails
the run.

Without a fixed plan, the finalizer advertises eligible terminal bookkeeping
tools. The finalizer can return a final response or select these terminal tools.
It cannot select a domain tool.

Each finalization tool receives `runtime.FinalizationReasonLabel`. The label
contains the exact termination reason. The runtime removes this reserved label
from ordinary tool calls and overwrites caller values during finalization.


### Runtime Policy Override

Override registered agent policy in-process:

```go
err := rt.OverridePolicy(agent.Ident("service.chat"), runtime.RunPolicy{
    MaxToolCalls:      10,
    MaxRecoveryTurns: 2,
    TimeBudget:        5 * time.Minute,
    InterruptsAllowed: true,
})
```

### Time budget and human waits

`TimeBudget` and `WithRunTimeBudget` limit active runtime work, not total
wall-clock age. Time spent waiting for clarification, confirmation, typed input,
or external tool results pauses the budget: the runtime extends both its budget
and hard deadlines by the measured wait duration. Manual human interaction can
therefore make elapsed wall time greater than `TimeBudget`.

`FinalizerGrace` and `WithRunFinalizerGrace` reserve time after the active
budget ends. The finalizer can return a response or execute an advertised
terminal bookkeeping tool.

The grace period does not permit normal planning or domain tool execution.
Deployments can enforce a separate wall-clock limit at the workflow boundary.

---

## Memory and Stores

### Memory Store

Stores a derived per-run transcript/event projection for planner context and
observability:

```go
type Store interface {
    LoadRun(ctx context.Context, agentID, runID string) (Snapshot, error)
    AppendEvents(ctx context.Context, agentID, runID string, events ...Event) error
}
```

**Event types:** `user_message`, `assistant_message`, `tool_call`, `tool_result`,
`planner_note`, `thinking`.

The runtime automatically installs a best-effort hook subscriber when a memory
store is configured. A memory append failure is recorded but does not make the
projection canonical or fail the run; use the run event log for the durable
hook record and see "Event reliability and authority" above.

The Mongo adapter writes each `AppendEvents` batch as a new immutable document
in `agent_memory_events`; it never grows one run document with `$push`. During
an upgrade it still reads the legacy `agent_memory` snapshot document, then
loads event buckets in `created_at`, `_id` order. The combined snapshot is
stable-sorted by event timestamp, preserving the `memory.Snapshot` chronological
contract; equal timestamps retain legacy-before-new order and deterministic
bucket order. Existing transcripts therefore remain visible without an offline
migration. New writes go only to the events collection. If
`Options.Collection` is customized, its default companion is
`<collection>_events`; `Options.EventsCollection` can override it explicitly.
Keep both collections through the compatibility window, and apply retention or
deletion to both.

Transcript projection is read-only: `Ledger.BuildMessages` includes the current
assistant turn without flushing or otherwise mutating the ledger. Workflow query
handlers such as `ledger_messages` can therefore inspect an in-progress turn
without splitting later text and tool-use parts into separate messages.

`Ledger` also implements `json.Marshaler` and `json.Unmarshaler`. Its JSON form
preserves both committed messages and the pending assistant message, including
typed thinking, text, tool-use, and tool-result parts. A workflow can therefore
round-trip the ledger itself through JSON-backed state and continue appending to
the same in-progress turn after restoration.

### Memory Search And Tools

Memory search has two surfaces:

- transcript/event search, backed by `memory.Store` and `memory.Searcher`;
- long-term entry search, backed by `memory.Service`.

`runtime.WithMemoryStore(...)` enables current-run snapshots.
`runtime.WithMemorySearcher(...)` enables indexed or cross-run event queries
through the `memory.Searcher` contract:

```go
type Searcher interface {
    Query(ctx context.Context, query memory.Query) (memory.QueryResult, error)
}
```

Generated memory-backed toolsets use `runtime.NewMemoryToolsetRegistration(...)`.
The model-facing `memory.load_memory` tool accepts `scope`, `event_types`, `labels`, and
`limit`, and returns `events`, `truncated`, and `scope`.

- `scope:"current_run"` uses `MemoryStore.LoadRun`.
- `scope:"indexed"` requires `WithMemorySearcher`; without it, the tool returns a structured
  tool error with retry hint reason `unsupported_operation`.

`runtime.WithMemoryService(...)` enables long-term entry memory through:

```go
type Service interface {
    IngestRun(ctx context.Context, input memory.IngestRunInput) (memory.IngestResult, error)
    IngestEvents(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error)
    PutEntry(ctx context.Context, input memory.PutEntryInput) (memory.Entry, error)
    Search(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error)
}
```

Long-term memory calls require a resolved `memory.Scope`. The default resolver
derives a non-global namespace from the agent ID and reads reserved run labels
`memory.namespace` and `memory.user_id`; production runtimes should provide
`runtime.WithMemoryScopeResolver(...)` for account/project/user routing.

Use `FromMemory(MemoryLongTerm(), ...)` to expose `search_memory`. The
model-facing payload accepts `query`, optional `labels`, and optional `limit`;
visibility and scope are owned by the design/runtime configuration, not by model
arguments. `MemoryVisibilityUser()` is the default, and
`MemoryVisibilityShared()` is explicit opt-in for shared memory. Results are
model-facing hits with content, author, public labels, score, and snippet; raw
scope, source references, and metadata stay inside the runtime service.

Run policy can also opt into bounded planner-input preload:

```go
RunPolicy(func() {
    PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
    PreloadLongTermMemory(MemoryVisibilityUser(), MemoryMaxResults(5))
})
```

Preloaded memory appears on `planner.PlanInput.PreloadedMemory` and
`planner.PlanResumeInput.PreloadedMemory`. Long-term preload appears separately
on `PreloadedMemoryEntries`, so planners can distinguish raw transcript events
from durable extracted entries. Nil policy preserves the default no-preload
behavior; planners should use explicit memory tools for follow-up lookup.
Planners that need prompt text can render long-term entries with
`memory.FormatEntriesForPrompt(...)`.

### Run event store (runlog.Store)

The runtime also maintains a canonical, append-only run event log used for
introspection, audit/debug UIs, and deriving compact `run.Snapshot` values.

```go
type Store interface {
    Append(ctx context.Context, e *runlog.Event) (runlog.AppendResult, error)
    List(ctx context.Context, runID string, cursor string, limit int) (runlog.Page, error)
}
```

`AppendResult.Inserted` distinguishes a new append from an exact idempotent
replay of the same `(run_id, event_key)`; conflicting bodies for one event key
fail.

The runtime exposes:

- `Runtime.ListRunEvents(ctx, runID, cursor, limit)` for cursor-paginated listing
- `Runtime.GetRunSnapshot(ctx, runID)` for a compact snapshot derived from replaying the run log

The snapshot projects every await variant (`await_clarification`,
`await_confirmation`, `await_questions`, `await_typed_input`,
`await_external_tools`) into `Snapshot.Await`, and maps `run_paused` /
`run_resumed` events to `Status` `"paused"` / `"running"`. A resumed or
completed run always reports `Await == nil`.

Configure the store via `runtime.WithRunEventStore(...)`. If not set, the runtime
defaults to an in-memory implementation (`runtime/agent/runlog/inmem`).

### Run Phases

Finer-grained lifecycle tracking for UIs:

```go
const (
    PhasePrompted       = "prompted"        // Input received
    PhasePlanning       = "planning"        // Planner deciding
    PhaseExecutingTools = "executing_tools" // Tools running
    PhaseSynthesizing   = "synthesizing"    // Final response
    PhaseCompleted      = "completed"
    PhaseFailed         = "failed"
    PhaseCanceled       = "canceled"
)
```

---

## History Policies

Control how conversation history is managed before each planner turn:

### KeepRecentTurns

Sliding window that preserves system messages and recent turns:

```go
// DSL
RunPolicy(func() {
    History(func() {
        KeepRecentTurns(20)
    })
})
```

### Compress

Model-assisted summarization for long conversations:

```go
// DSL
RunPolicy(func() {
    History(func() {
        CompressAtMaxInputTokens(120000)
        KeepMaxInputTokens(40000)
        KeepMaxTurns(12)
    })
})

// Registration
cfg := chat.ChatAgentConfig{
    Planner:      myPlanner,
    HistoryModel: smallModelClient, // For compression
}
```

Compression triggers are explicit. Use `CompressAtTurns` for a turn-count
threshold, `CompressAtMaxInputTokens` for an exact provider input-token
threshold, or both. Retention is explicit too: `KeepMaxTurns` caps the newest
exact turns, while `KeepMaxInputTokens` keeps newest whole turns that fit within
the measured token budget. Token-budget compression requires a history model
that implements `model.TokenCounter`; approximate estimators are rejected for
hard compression decisions.

---

## Prompt Caching

Configure automatic cache checkpoint placement:

```go
// DSL
RunPolicy(func() {
    Cache(func() {
        AfterSystem()  // Checkpoint after system messages
        AfterTools()   // Checkpoint after tool definitions
    })
})
```

The runtime populates `model.Request.Cache` when planners don't set it explicitly.
The Anthropic and Bedrock adapters map `AfterSystem`, `AfterTools`, and explicit
`model.CacheCheckpointPart` boundaries to their native ephemeral prompt-cache
controls. Policy boundaries are omitted when the corresponding system or tool
section is empty. Providers that don't support caching ignore these options.

---

## System Reminders

Deliver structured, rate-limited guidance to models:

```go
input.Agent.AddReminder(reminder.Reminder{
    ID:              "pending_todos",
    Text:            "Review pending todo items before proceeding.",
    Priority:        reminder.TierGuidance,
    Attachment:      reminder.Attachment{Kind: reminder.AttachmentUserTurn},
    MaxPerRun:       3,
    MinTurnsBetween: 2,
})

// Remove when no longer relevant
input.Agent.RemoveReminder("pending_todos")
```

**Tiers:**

| Tier           | Purpose                             |
| -------------- | ----------------------------------- |
| `TierSafety`   | Never suppressed (P0)               |
| `TierGuidance` | Soft nudges, first to suppress (P2) |

---

## Model Clients

Provider packages under `features/model/*` construct raw `model.Provider`
implementations. Runtime factory methods wrap them with `model.NewClient` and
return validated `model.Client` values. If an application constructs a provider
directly, it must call `model.NewClient(provider)` before registration. The
different `Stream` return types make a raw provider fail the `model.Client`
interface at compile time.

### Registration

```go
// Register model clients before Seal. RegisterModel returns
// ErrRegistrationClosed after Seal or the first submitted run.
err := rt.RegisterModel("bedrock", bedrockClient)

// Create Bedrock client via runtime helper
client, err := rt.NewBedrockModelClient(awsClient, runtime.BedrockConfig{
    DefaultModel:   "us.anthropic.claude-3-5-sonnet-20240620-v1:0",
    HighModel:      "us.anthropic.claude-3-opus-20240229-v1:0",
    SmallModel:     "us.anthropic.claude-3-haiku-20240307-v1:0",
    MaxTokens:      4096,
    ThinkingBudget: 10000,
})
```

Model lookup is immutable after registration closes. Loom does not define
post-`Seal` credential/client rotation or in-flight hot-swap semantics; build a
new runtime and move traffic to it when replacing a model client.

Validated clients bound request and response ownership, enforce the exact
current tool catalog and tool-choice rule, run structured-output and generated
completion validation, reject output-limit termination, and expose only
terminally accepted tool calls. `model.OutputValidationError` has a closed kind
and bounded fingerprint; its public message does not render rejected model text,
tool names, arguments, or schemas.

Create an OpenAI Responses client through the runtime helper:

```go
client, err := rt.NewOpenAIModelClient(runtime.OpenAIConfig{
    APIKey:       os.Getenv("OPENAI_API_KEY"),
    DefaultModel: "gpt-4.1",
    HighModel:    "gpt-4.1",
    SmallModel:   "gpt-4.1-mini",
})
```

If `model.Request.Thinking.Enable` is true, the OpenAI adapter requests an
automatic reasoning summary. It returns each summary as a typed
`model.ThinkingPart`.

Explicit `model.Request.Model` values take precedence over class routing; when
the request leaves `Model` empty, `ModelClass` selects `HighModel` or
`SmallModel`, falling back to `DefaultModel` when no class-specific model is
configured.

Create a local Ollama chat client through the runtime helper. Ollama uses the
`/api/chat` endpoint and supports text, images, streaming text, function tools,
native thinking output, and schema-backed structured output for models that
support those features. When `model.Request.Thinking` is set, the adapter maps
it to Ollama's top-level `think` flag and surfaces `message.thinking` as typed
`model.ThinkingPart` / `model.ChunkTypeThinking` content instead of assistant
text. Some Gemma 4 variants, including MLX builds, also require the model-level
`<|think|>` control token at the start of the system prompt to activate
thinking; keep that prompt concern separate from the adapter's response-side
typed thinking contract:

```go
client, err := rt.NewOllamaModelClient(runtime.OllamaConfig{
    ServerURL:    "http://localhost:11434",
    DefaultModel: "llama3.1",
    HighModel:    "qwen3:32b",
    SmallModel:   "llama3.2",
    MaxTokens:    4096,
})
```

Gemini is backed by Google's official `google.golang.org/genai` SDK. Use the
Gemini API helper for API-key deployments:

```go
client, err := rt.NewGeminiModelClient(ctx, runtime.GeminiConfig{
    APIKey:         os.Getenv("GEMINI_API_KEY"),
    DefaultModel:   "gemini-2.5-flash",
    HighModel:      "gemini-2.5-pro",
    SmallModel:     "gemini-2.5-flash-lite",
    MaxTokens:      4096,
    ThinkingBudget: 8192,
})
```

Use the Vertex helper when Google Cloud project and location should own auth and
routing. The Gen AI SDK uses Application Default Credentials for the Vertex
backend unless `APIKey` or explicit `Credentials` are set:

```go
client, err := rt.NewVertexGeminiModelClient(ctx, runtime.VertexConfig{
    ProjectID:      "my-gcp-project",
    Location:       "global",
    DefaultModel:   "gemini-2.5-pro",
    SmallModel:     "gemini-2.5-flash",
    MaxTokens:      4096,
    ThinkingBudget: 8192,
})
```

### Structured Output

`model.Request.StructuredOutput` asks the provider to enforce a JSON Schema for
the assistant response:

```go
resp, err := modelClient.Complete(ctx, &model.Request{
    Messages: input.Messages,
    StructuredOutput: &model.StructuredOutput{
        Name: "draft",
        Schema: []byte(`{
            "type": "object",
            "required": ["title", "summary"],
            "properties": {
                "title": {"type": "string"},
                "summary": {"type": "string"}
            }
        }`),
    },
})
```

Gemini, OpenAI, Bedrock, and Ollama `Complete` map this to provider-native or
provider-supported schema controls. Bedrock, OpenAI, and Ollama also support
streamed structured output. Anthropic rejects structured output, and Gemini
does not implement streaming. Structured output cannot be combined with model
tools in the current Gemini, OpenAI, and Ollama adapters.

### Provider capability matrix

This table describes the adapter contract in this repository. Individual model
families may impose additional provider-side restrictions.

| Provider | Complete | Stream | Structured output | Tool choice | Cache checkpoints | Thinking/reasoning | Exact `TokenCounter` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Anthropic | yes | yes | no | yes | yes | typed thinking and redacted thinking | no |
| Bedrock | yes | yes | unary and stream | yes | yes | typed thinking, including adaptive models | yes |
| OpenAI Responses | yes | yes | unary and stream; not with tools | yes | ignored on replay | reasoning summaries become typed thinking | no |
| Gemini / Vertex | yes | no (`ErrStreamingUnsupported`) | unary; not with tools | yes | no | yes | yes |
| Ollama | yes | yes | unary and stream; not with tools | limited by local API/model | no | native typed thinking | no |

Every provider runs `testutil.RunProviderConformance`. The suite requires one
supported or unsupported case for each provider capability. These capabilities
include multimodal input, typed thinking, exact token counting, and tool-name
round trips. Streaming providers prove setup, receive, terminal, and
receive-time rate-limit behavior.

Streaming provider adapters assemble tool arguments only until the provider
closes the content block. Anthropic and Bedrock normalize empty arguments to
`{}`; OpenAI preserves its existing empty-argument representation. Malformed
or truncated JSON fails the stream with a provider-scoped error instead of
being replaced with an empty object.

Bedrock normalizes structured-output schemas to its supported subset. It closes
object schemas, removes unsupported keywords and formats, and rejects shapes
whose `additionalProperties` semantics cannot be represented instead of
silently weakening the schema. Its exact token counter uses the provider's
count-tokens path and removes replayed thinking blocks before counting. Unary
and streaming responses both preserve provider reasoning as typed thinking.

The OpenAI adapter projects tool and structured-output schemas into the
provider's strict-mode JSON Schema subset and canonicalizes strict-mode `null`
omissions back to absent fields before returning tool or structured payloads.
Like the Anthropic and Bedrock adapters, it also translates canonical dotted
tool identifiers (`toolset.tool`) into provider-safe function names
(`[a-zA-Z0-9_-]`, at most 64 characters) on the wire and maps returned
function-call names back to canonical identifiers, failing fast when two tool
names sanitize to the same provider name.

Tool names and tool-use correlation IDs use separate request-scoped codecs.
When Anthropic or Bedrock replays a transcript, provider-safe tool-use IDs pass
through unchanged and reserve their wire values before unsafe internal IDs are
assigned synthetic `tN` values. Synthetic allocation skips every occupied ID,
so a safe `t1` cannot collide with a run-scoped ID that requires substitution;
tool-use and tool-result blocks retain the same correlation ID.

Construct the direct Anthropic adapter with the default SDK client or an
application-owned Messages client:

```go
client, err := anthropic.NewFromAPIKey(
    os.Getenv("ANTHROPIC_API_KEY"),
    "claude-sonnet-4-20250514",
)
```

Use `anthropic.New(messagesClient, anthropic.Options{...})` when the application
owns SDK configuration. Anthropic supports tools, streaming, prompt-cache
checkpoints, and thinking blocks, but not structured output or exact token
counting.

When planners render prompts through `RenderPrompt`, copy prompt provenance into model requests:

```go
content, err := input.Agent.RenderPrompt(ctx, "aura.chat.system", map[string]any{
    "AssistantName": "Ops Assistant",
})
if err != nil {
    return nil, err
}

resp, err := modelClient.Complete(ctx, &model.Request{
    RunID:      input.RunContext.RunID,
    Messages:   input.Messages,
    PromptRefs: []prompt.PromptRef{content.Ref},
})
```

### Rate Limiting

Apply adaptive rate limiting:

```go
import mdlmw "github.com/CaliLuke/loom-mcp/v2/features/model/middleware"

rl := mdlmw.NewAdaptiveRateLimiter(
    ctx,
    throughputMap,     // *rmap.Map for cluster-wide state (nil for local)
    "bedrock:sonnet",  // Model family key
    80_000,            // Initial TPM
    1_000_000,         // Max TPM
)

limitedClient := rl.Middleware()(rawClient)
rt.RegisterModel("bedrock", limitedClient)
```

`NewAdaptiveRateLimiter` preserves its estimated-input-only admission contract.
Use `NewOutputReservationAdaptiveRateLimiter` when every request sets a
positive `MaxTokens` value and the provider implements exact
`model.TokenCounter`; that mode reserves both values before the provider call.

---

## Run Options

Customize run behavior with functional options:

```go
client.Run(ctx, "session-1", msgs,
    runtime.WithRunID("custom-run-id"),
    runtime.WithTurnID("turn-1"),
    runtime.WithLabels(map[string]string{"tenant": "acme"}),
    runtime.WithMetadata(map[string]any{"request_id": "abc"}),
    runtime.WithTaskQueue("custom-queue"),
    runtime.WithMemo(map[string]any{"workflow_name": "Chat"}),
    runtime.WithSearchAttributes(map[string]any{"tenant": "acme"}),
    runtime.WithTiming(runtime.Timing{
        Budget: 2 * time.Minute,
        Plan:   30 * time.Second,
        Tools:  60 * time.Second,
    }),
)
```

Search attributes are passed through to the workflow engine as caller-owned
index metadata. The runtime does not mirror `SessionID` into engine search
attributes automatically.

`Timing.Plan` and `Timing.Tools` are semantic attempt budgets. They bound how
long a healthy planner or tool attempt may run once execution starts. Queue-wait
timeouts and heartbeat-based liveness detection are engine-specific concerns and
belong in the engine adapter, not the generic runtime API.

---

## Introspection

Query registered agents and tools:

```go
// List registered agents
agents := rt.ListAgents()  // []agent.Ident

// List registered toolsets
toolsets := rt.ListToolsets()  // []string

// Get tool spec
spec, ok := rt.ToolSpec(tools.Ident("helpers.search"))

// Get parsed tool schema
schema, ok := rt.ToolSchema(tools.Ident("helpers.search"))

// Get specs for an agent
specs := rt.ToolSpecsForAgent(agent.Ident("service.chat"))
```

---

## Engine Integration

### Engine Interface

```go
type Engine interface {
    RegisterWorkflow(ctx, def WorkflowDefinition) error
    RegisterHookActivity(ctx, name, opts, fn) error
    RegisterPlannerActivity(ctx, name, opts, fn) error
    RegisterExecuteToolActivity(ctx, name, opts, fn) error
    StartWorkflow(ctx, req WorkflowStartRequest) (WorkflowHandle, error)
    QueryRunStatus(ctx, runID string) (RunStatus, error)
}
```

### WorkflowContext

Workflow handlers receive a context for deterministic operations:

```go
type WorkflowContext interface {
    Context() context.Context
    WorkflowID() string
    RunID() string
    Now() time.Time  // Deterministic time
    PublishHook(ctx, call) error
    ExecutePlannerActivity(ctx, call) (*api.PlanActivityOutput, error)
    ExecuteToolActivity(ctx, call) (*api.ToolOutput, error)
    ExecuteToolActivityAsync(ctx, call) (Future[*api.ToolOutput], error)
    PauseRequests() Receiver[api.PauseRequest]
    ResumeRequests() Receiver[api.ResumeRequest]
    ClarificationAnswers() Receiver[api.ClarificationAnswer]
    ExternalToolResults() Receiver[api.ToolResultsSet]
    ConfirmationDecisions() Receiver[api.ConfirmationDecision]
    StartChildWorkflow(ctx, req) (ChildWorkflowHandle, error)
    SetQueryHandler(name, handler) error
}
```

Temporal signal receivers select between the signal channel and workflow
cancellation. Canceling a workflow therefore releases a receiver blocked in
`Receive` or `ReceiveWithTimeout`, and durable completion records the terminal
status as `canceled` when all error leaves are cancellations. An independent
cleanup, provider, hook, or transport failure makes the run fail.

### Available Engines

**Temporal worker** — Production-grade durable execution:

```go
import temporal "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"

eng, _ := temporal.NewWorker(temporal.Options{
    ClientOptions: &client.Options{
        HostPort:  "temporal:7233",
        Namespace: "default",
    },
    WorkerOptions: temporal.WorkerOptions{
        TaskQueue: "orchestrator.chat",
    },
    ActivityDefaults: temporal.ActivityDefaults{
        Planner: temporal.ActivityTimeoutDefaults{
            QueueWaitTimeout: 30 * time.Second,
            LivenessTimeout:  20 * time.Second,
        },
        Tool: temporal.ActivityTimeoutDefaults{
            QueueWaitTimeout: 2 * time.Minute,
            LivenessTimeout:  20 * time.Second,
        },
    },
})
```

**Temporal client** — Start/query/signal without local polling:

```go
eng, _ := temporal.NewClient(temporal.Options{
    ClientOptions: &client.Options{
        HostPort:  "temporal:7233",
        Namespace: "default",
    },
})
```

In this split:

- `RunPolicy.Timing.Plan` / `runtime.WithTiming(...).Plan` set the planner
  attempt budget.
- `RunPolicy.Timing.Tools` / `runtime.WithTiming(...).Tools` set the tool
  attempt budget.
- `temporal.Options.ActivityDefaults` sets Temporal-only queue-wait and
  heartbeat liveness behavior.

**In-memory** — Fast iteration, no durability:

```go
import inmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"

eng := inmem.New()
```

---

## Telemetry

### Logger Interface

```go
type Logger interface {
    Debug(ctx context.Context, msg string, keyvals ...any)
    Info(ctx context.Context, msg string, keyvals ...any)
    Warn(ctx context.Context, msg string, keyvals ...any)
    Error(ctx context.Context, msg string, keyvals ...any)
}
```

### Metrics Interface

```go
type Metrics interface {
    IncCounter(name string, value float64, tags ...string)
    RecordTimer(name string, duration time.Duration, tags ...string)
    RecordGauge(name string, value float64, tags ...string)
}
```

### Tracer Interface

```go
type Tracer interface {
    Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, Span)
    Span(ctx context.Context) Span
}
```

`runtime.WithMetrics` and `runtime.WithTracer` drive semantic runtime
instrumentation independently of the selected workflow engine. The stable core
metrics are:

| Metric | Kind | Dimensions | Meaning |
| --- | --- | --- | --- |
| `loom_mcp.runtime.run.started` | counter | `agent` | Canonical run-start events |
| `loom_mcp.runtime.run.completed` | counter | `agent`, `status` | Canonical terminal run events |
| `loom_mcp.runtime.planner.attempts` | counter | `agent`, `operation`, `status` | Planner activity attempts (`start` or `resume`) |
| `loom_mcp.runtime.planner.duration` | timer | `agent`, `operation`, `status` | Planner attempt duration |
| `loom_mcp.runtime.tool.completed` | counter | `agent`, `tool`, `status` | Canonical tool-result events |
| `loom_mcp.runtime.tool.duration` | timer | `agent`, `tool`, `status` | Tool duration reported by the canonical result |

Run and tool metrics are emitted only when the canonical run log inserts a new
event key, so normal hook-activity retries do not double-count them. Planner
metrics are deliberately attempt-level and may repeat when the workflow engine
retries a planner activity. Metric dimensions are bounded registry/outcome
values; run, session, turn, and tool-call IDs are trace attributes rather than
metric dimensions.
Unregistered or model-invented tool names use the metric value `unknown`; the
raw name remains available on the corresponding span.

Planner calls use `planner.plan_start` and `planner.plan_resume` spans. Every
new canonical tool result also emits a `tool.execute` semantic span covering
the result's reported execution interval, with `loom_mcp.agent_id`,
`loom_mcp.run_id`, `loom_mcp.session_id`, `loom_mcp.turn_id`,
`loom_mcp.tool.name`, and `loom_mcp.tool_call_id` attributes. Because this
instrumentation runs at planner activity and canonical hook boundaries, it is
the same for in-memory, Temporal, and custom engines. Nil telemetry options
still resolve to no-op implementations.

Model calls emit both Loom-specific correlation attributes (`loom_mcp.*`) and
standard OpenTelemetry GenAI attributes (`gen_ai.*`) on `model.complete` and
`model.stream` spans. Token usage, finish reasons, requested model, resolved
model, and streaming time-to-first-chunk are recorded as span attributes.

Full GenAI input/output message capture is available but disabled by default
because message payloads can contain sensitive data:

```go
rt := runtime.New(
    runtime.WithTracer(telemetry.NewClueTracer()),
    runtime.WithCaptureGenAIMessages(true),
)
```

When enabled, model spans include `gen_ai.input.messages` and
`gen_ai.output.messages`. Reasoning/thinking parts are deliberately omitted,
streamed text deltas are coalesced into one output message at stream end, and
serialization failures are recorded as span events instead of failing the model
call. Capture uses the request after cache, tool policy, and model interceptors
apply their changes. A rejected response or stream does not publish output
messages. Internal `runtime.tool_unavailable` payloads contain only the exact
effective tool catalog.

### Temporal trace domains

Temporal engine tracing and metrics are enabled by default. Set
`temporal.InstrumentationOptions.DisableTracing` or `DisableMetrics` to opt out;
`MetricsOptions` configures the Temporal OTEL metrics handler.

Durable scheduling is a trace-domain boundary. Each Temporal activity starts a
new root span with a new trace ID and attaches the originating request span as
an OpenTelemetry link. Activities are therefore correlated with, but are not
children in, one long request trace. This avoids treating queue and replay time
as synchronous parent/child latency. `InstrumentationOptions.TracerOptions` is
retained for source compatibility but is ignored by this trace-domain
implementation.

This engine instrumentation is separate from `runtime.WithTracer`. That option
owns runtime spans, model spans, generated MCP adapter tracing, and the SDK
`TransportObserver`. The local debug server and Pulse stream sink are separate
surfaces.

---

## Feature Modules

| Package                     | Purpose                              |
| --------------------------- | ------------------------------------ |
| `features/memory/mongo`     | MongoDB-backed memory store          |
| `features/prompt/mongo`     | MongoDB-backed prompt override store |
| `features/runlog/mongo`     | MongoDB-backed run event log store   |
| `features/session/mongo`    | MongoDB-backed session store         |
| `features/stream/pulse`     | Pulse message bus sink               |
| `features/model/bedrock`    | AWS Bedrock model client             |
| `features/model/openai`     | OpenAI-compatible model client       |
| `features/model/anthropic`  | Direct Anthropic Claude API client   |
| `features/model/ollama`     | Local Ollama chat model client       |
| `features/model/gemini`     | Google Gemini API and Vertex AI model client |
| `features/model/gateway`    | Remote model gateway client          |
| `features/model/middleware` | Rate limiting, logging, metrics      |
| `features/policy/basic`     | Basic policy engine                  |

---

## MCP Callers

The `runtime/mcp` package provides three caller implementations for different MCP server
transports. Each constructor establishes an initialized MCP session and returns a
caller that can be reused across multiple tool invocations.

Generated SDK-backed MCP servers also expose server-to-client features through
request context. `mcp.Elicit` requests structured user input through official
multi-round-trip `inputRequests`/`inputResponses`, so the implementation is
re-entered and code before the runtime helper must be retry-safe.
`mcp.ReportProgress` sends request-scoped progress notifications using the
original client token. Both fail closed with an unavailable error outside a
generated SDK request context. Sampling and roots are deprecated in MCP
`2026-07-28` and are not exposed as Loom runtime client features. Multi-step
elicitation requires a `2026-07-28` client and uses one round trip per
`mcp.Elicit` call. Its `requestState` is AES-GCM protected, bound to the
original logical request, and portable across SDK server replicas that share a
stable 32-byte `SDKServerOptions.RequestStateKey`. The protected responses
remain client assertions, not an authorization or server-state channel. See
[`mcp_sdk_server.md`](mcp_sdk_server.md) for the generated-server contracts and
examples.

### StdioCaller

Spawns an MCP server as a subprocess and communicates via stdin/stdout:

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"

caller, err := mcp.NewStdioCaller(ctx, mcp.StdioOptions{
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-filesystem"},
    Env:     []string{"HOME=" + os.Getenv("HOME")},
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

### HTTPCaller

Connects to an MCP server that exposes the streamable HTTP transport:

```go
caller, err := mcp.NewHTTPCaller(ctx, mcp.HTTPOptions{
    Endpoint:    "https://mcp-server.example.com/mcp",
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

### SSECaller

Connects to an MCP server that exposes the legacy SSE transport:

```go
caller, err := mcp.NewSSECaller(ctx, mcp.HTTPOptions{
    Endpoint:    "https://mcp-server.example.com/sse",
    InitTimeout: 5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}
defer caller.Close()
```

All three constructors return a caller that implements `mcp.Caller`. The
initialization timeout only bounds session establishment; it does not cancel the
live session after `Connect` succeeds.

### Normalized tool results

MCP callers normalize `tools/call` results into a consistent shape:

- Text content items are concatenated in order.
- If the combined text is valid JSON, it becomes `CallResponse.Result`.
- Otherwise the combined text is returned as a JSON string.
- `CallResponse.Structured` contains the full structured MCP content payload.
- If a tool returns no text content, callers fall back to marshaling the first
  structured content item into `CallResponse.Result`.

All outbound MCP callers use the official SDK and the same normalization
contract.

The same package also exposes canonical JSON helpers used at MCP boundaries.
Those helpers accept string-keyed maps, including named string aliases, and
fail fast on unsupported map key kinds instead of silently dropping entries.

### SDK transport ownership

The official MCP Go SDK owns protocol negotiation, Streamable HTTP sessions,
cancellation, standard SSE, and wire-level JSON-RPC. Loom does not generate a
native MCP client or server transport.

The generated SDK wrapper binds each session to one verified principal. It
checks that binding on POST, GET, and DELETE requests. Authentication
middleware must wrap the generated handler.
### Generated MCP tool search

Generated MCP adapters can opt into progressive discovery with
`MCPAdapterOptions.ToolSearch`. Disabled mode keeps the full generated
`tools/list` catalog. Enabled mode makes `tools/list` the authoritative compact
public surface: it returns synthetic `search_tools` and `call_tool` entries plus
real tools named in `ToolSearchOptions.AlwaysVisible`.

`search_tools` accepts either a plain `query` or a case-insensitive regex
`pattern`, never both. It also accepts `category`, `tags`, `max_results`, and
`include_schemas`. Plain query matching normalizes snake_case names into words,
singularizes simple plurals, drops common instruction words, and ranks matches
before limiting. The default policy prioritizes exact and normalized tool-name
or title matches, then prefix/contains name and title matches, then fuzzy
name/title matches using generated token scoring, then broader discovery
metadata, description, parameter, and schema matches. In the default `narrow`
exact-match mode, high-confidence name/title matches suppress weaker broad
and fuzzy matches. High-confidence includes exact, normalized, prefix, and
contains tiers, so a query such as `sentiment` does not retain an unrelated
fuzzy subsequence candidate beside `analyze_sentiment`.

Products can tune generated defaults in the MCP DSL with
`ToolSearch(...)`: `ToolSearchMaxResults`, `ToolSearchMinScore`,
`ToolSearchExactMatch(ToolSearchExactMatchNarrow|ToolSearchExactMatchBoost|ToolSearchExactMatchOff)`,
`ToolSearchFuzzyNameMatching`, `ToolSearchBroadFallback`, and
`ToolSearchWeights(...)`. Generated `ToolSearchOptions` expose the same runtime
knobs for deployment-specific overrides.

The result includes model-readable text with exact `call_tool` JSON examples,
not only a prose invocation hint. Structured content includes `tools`,
`total_matches`, `truncated`, and the supplied `query` or `pattern`. Tool
descriptors preserve MCP Tool-shaped fields including `inputSchema`,
`outputSchema`, `_meta`, `annotations`, and `icons`; schemas are omitted by
default and included only when `include_schemas` is true. Each descriptor also
includes `why_matched`, `call_tool_name`, `call_tool_arguments`, and
`call_tool_json` so clients and models can invoke the hidden target through the
wrapper without guessing the schema. Tool declarations can use
`ToolDiscoveryCallTemplateArg` to include useful optional arguments in those
examples without changing payload validation.

`call_tool` invokes a discovered real tool by name with an `arguments` object. It
rejects synthetic targets and unknown real tool names as tool errors. In compact
mode, direct public `tools/call` requests for hidden real tools are rejected by
default. Pinned `AlwaysVisible` tools remain directly callable. The local
adapter can opt into `ToolSearchOptions.AllowDirectHiddenCalls` as a
compatibility option. SDK-backed servers reject that option at construction
because unregistered SDK tools cannot preserve authoritative compact
discovery. Synthetic name collisions and unknown `AlwaysVisible` pins fail
fast during adapter construction.

Toolset tools projected into MCP participate in the same generated catalog as
method-level MCP tools. Their `ToolInfo` schemas come from the generated toolset
`tools.ToolSpec` payload and result schemas, and their `tools/call` cases route
through the shared method-backed dispatcher. Compact discovery treats projected
tools like any other real tool: they may be pinned with `AlwaysVisible`, found
through `search_tools`, and invoked through `call_tool`.

For agents in the same Go process, codegen also emits
`New<Service><MCP>LocalToolsetRegistration(adapter)`. The returned
`runtime.ToolsetRegistration` exposes the same synthetic tools and
`AlwaysVisible` pins as the adapter's compact `tools/list`. Search and
`call_tool` run directly through the adapter's catalog, interceptors, and
generated method and projected dispatchers. They do not open an HTTP
connection, initialize MCP, or create session state. The registration converts
structured MCP results and tool errors into ordinary planner-visible tool
results.

```go
adapter := mcpassistant.NewMCPAdapter(service, promptProvider,
    &mcpassistant.MCPAdapterOptions{
        ToolSearch: &mcpassistant.ToolSearchOptions{
            AlwaysVisible: []string{"search"},
        },
    })
localTools, err := mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
if err != nil {
    return err
}
if err := rt.RegisterToolset(localTools); err != nil {
    return err
}
```

### MCP resource authorization boundaries

Generated adapters apply `MCPAdapterOptions.AllowedResourceURIs`,
`AllowedResourceNames`, `DeniedResourceURIs`, and `DeniedResourceNames` to DSL
resources and `skill://` resources. Exact URI policies match one resource;
policies ending in `/` match a URI prefix. A skill name resolves to its
`skill://<name>/` prefix.

Adapter allow policies are the server's maximum grant. Request-scoped allowed
names are a separate narrowing constraint. When both exist, a resource must
satisfy both. Request and adapter deny policies are additive and take
precedence over every allow.

Headers are untrusted input and do not authenticate a caller or create a
grant. Applications must authenticate before the generated handler, derive any
principal or tenant policy from verified credentials, and configure the
adapter's maximum grant from trusted deployment policy. OAuth DSL declarations
generate metadata, challenges, and audience helpers; they do not install this
application authorization layer.

### Resource updates

Declare `WatchableResource` to enable standard MCP subscriptions. Call the
generated `SDKServer.ResourceUpdated(ctx, uri)` method after the resource
changes. Watchable resources require stateful Streamable HTTP sessions.

`MCPAdapterOptions.ToolCallInterceptors` wrap generated `tools/call` execution
in declaration order, with the first interceptor outermost. Each interceptor
may short-circuit or invoke `next` and receives the tool name plus raw arguments.
Those arguments may contain credentials or user data; do not log them by
default.

### Repair prompts for invalid params (retry.RetryableError)

When an MCP server reports invalid parameters and a structured repair prompt is available, generated
clients may return `retry.RetryableError` with a deterministic `Prompt`. This is intended for LLM-driven
correction: the model returns JSON-only corrected params, which are decoded into the operation payload and retried.

---

## OAuth 2.0 Protected Resource

When the MCP design declares `OAuth(...)`, loom-mcp generates spec-compliant
discovery and challenge plumbing. Runtime helpers in `runtime/mcp/oauth.go`
back that generated code. Consumers interact with three surfaces: the DSL,
the generated helpers, and the runtime primitives below.

Declared `OAuthScope` values populate protected-resource metadata and Bearer
challenges. They do not implement per-operation scope authorization; enforce
required scopes in the application's verifier or authorization middleware.

### Canonicalization

Two functions derive URLs used in PRM and challenges. They differ in how
they handle forwarded headers and failure.

- `CanonicalizeResourceURL(r, trustProxy bool) (string, error)` — strict.
  Derives the RFC 8707 canonical resource URI. When `trustProxy` is false
  (the default for any generated server without `TrustProxyHeaders()` in
  the DSL), forwarded headers are ignored entirely and the origin is
  derived from `r.Host` + `r.TLS` only. When `trustProxy` is true,
  `X-Forwarded-Proto`, `X-Forwarded-Host`, and RFC 7239 `Forwarded` are
  consumed; a header present but malformed is rejected with
  `ErrInvalidForwardedHeaders`. Callers MUST surface errors as 400 Bad
  Request — never silently substitute `r.Host`.
- `CanonicalizeChallengeOrigin(r, trustProxy bool) string` — lenient.
  Used for challenge emission only. Never returns an error: if the strict
  canonicalizer would fail, the function falls back to `r.Host` + `r.TLS`
  so the 401 response still carries some `resource_metadata` URL. This is
  deliberate — a challenge is a formatting artifact inside an already-401
  response, not an identity claim.

### Challenge helpers

- `BuildBearerChallenge(resourceMetadataURL, scope string) string` — RFC 6750
  §3 Bearer challenge. Scope parameter is omitted when empty.
- `BuildInvalidTokenChallenge(resourceMetadataURL, errorDescription string) string` —
  RFC 6750 §3.1 `invalid_token` challenge. Used when a decoded token
  fails audience binding, is expired, or is revoked.
- `WriteUnauthorized(w, resourceMetadataURL, scope string)` —
  convenience wrapper that writes the 401 + JSON body + challenge.
- `WriteInvalidToken(w, resourceMetadataURL, errorDescription string)` —
  same, but for the `invalid_token` form.

`escapeHeaderQuoted` strips CR and LF from header values — defense in depth
against response-splitting even if a misconfigured DSL constant or bypassed
canonicalizer feeds a URL with raw newlines into a challenge builder.

### WithOAuthChallenge middleware

```go
handler := mcpruntime.WithOAuthChallenge(
    mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
    "/rpc",
    mcpassistant.OAuthChallengeHeader,
)
```

Intercepts responses and augments any 401 that does NOT already carry a
`resource_metadata=` parameter (case-insensitively, per RFC 7235) with the
spec-compliant challenge from `challenge(r, mountPath)`. Existing upstream
challenges are preserved verbatim, so consumer middleware can still override
the default. The interceptor passes through `Flush()` so SSE streaming is
unaffected.

The middleware is error-agnostic. It cannot tell a missing token from an
expired, revoked, or wrong-audience token, so it does not add
`error="invalid_token"` automatically. Error-aware application auth middleware
can use the generated `OAuthInvalidTokenChallengeHeader` or runtime
`WriteInvalidToken` helper when that RFC 6750 distinction is required.

### Audience binding

When the DSL declares `ResourceIdentifier(...)`, the generated package
exposes `EnforceAudience(base mcpauth.TokenVerifier) mcpauth.TokenVerifier`.
Wrap the consumer verifier once at mount time:

```go
verifier := mcpassistant.EnforceAudience(consumerVerifier)
handler := mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler)
```

The wrapper reads `TokenInfo.Extra["aud"]` and accepts `string`,
`[]string`, and `[]any` (the shape JSON decoding produces for
a JWT `aud` array). Missing or wrong-typed claims fail closed — there is
no silent admission path. Mismatches return `ErrAudienceMismatch`, which
wraps `mcpauth.ErrInvalidToken` so `RequireBearerToken` responds 401 and
`WithOAuthChallenge` adds the standard resource-metadata Bearer challenge. It
does not add the `invalid_token` parameter automatically.

Without `ResourceIdentifier(...)`, the framework cannot know the expected
audience (it would be per-request), so `EnforceAudience` is not generated.
Audience enforcement in that mode remains the consumer's responsibility.

### Forwarded-header trust posture

loom-mcp's default is to NOT trust `X-Forwarded-*` or RFC 7239 `Forwarded`
headers. Enable `TrustProxyHeaders()` in the DSL only when every request
reaches the server through a reverse proxy the operator fully controls and
that strips these headers from direct-client requests. Otherwise an
attacker with direct network access can set forwarded headers to control
the PRM `resource` field advertised to clients.

For most deployments, pinning `ResourceIdentifier(...)` is preferred. A
declared identifier bypasses forwarded-header derivation entirely and is
the spec's recommended posture. The PRM handler also short-circuits the
strict canonicalizer when the identifier is pinned, so a malformed
forwarded header cannot fail the PRM request in that configuration.

### Sentinels

- `ErrInvalidForwardedHeaders` — a `Forwarded` or `X-Forwarded-*` header was
  present but malformed (control characters, path/fragment/userinfo
  delimiters, etc.). Surface as 400.
- `ErrEmptyResourceURL` — no scheme+host could be derived. Surface as 400
  with a diagnostic message; if this fires in steady state, pin
  `ResourceIdentifier` or enable `TrustProxyHeaders()` as appropriate for
  the deployment.

---

## Stream Profile Reference

Stream profiles control which events reach different audiences. Use profiles to filter
events for specific use cases.

| Profile               | Purpose                       | Events Included          |
| --------------------- | ----------------------------- | ------------------------ |
| `DefaultProfile()`    | All events, child runs linked | All event types          |
| `UserChatProfile()`   | End-user chat UIs             | Same as default          |
| `AgentDebugProfile()` | Debug view                    | All event types          |
| `MetricsProfile()`    | Telemetry and monitoring      | `usage`, `workflow` only |

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"

// Get a profile
profile := stream.AgentDebugProfile()

// Profiles are used internally by stream subscribers
// to filter events before delivery
```

---

## Tool Errors

The `runtime/agent/toolerrors` package provides structured error types for tool execution
failures that integrate with the planner retry system.

```go
import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/toolerrors"

// Create a tool error with retry hint
err := toolerrors.New(
    toolerrors.WithMessage("Database connection failed"),
    toolerrors.WithRetryable(true),
    toolerrors.WithHint("Check database connectivity and retry"),
)

// Check if error is retryable
if toolerrors.IsRetryable(err) {
    // Handle retry logic
}

// Tool errors are automatically converted to planner.RetryHint
// for planners to handle gracefully
```

### Validation Issues and Retry Hints

Tool calls can fail because the input payload is missing fields, violates constraints,
or has the wrong JSON shape. When that happens, callers generally need actionable,
field-level feedback rather than a generic failure string.

Loom MCP supports two complementary paths that produce `planner.RetryHint`:

1. **Decode‑time validation (generated codecs)**

   The generated tool codec validates the tool JSON payload before execution.
   If validation fails, the codec returns a generated validation error that exposes
   structured issues (`Issues() []*tools.FieldIssue`) and descriptions. The runtime
   converts these into `planner.RetryHint` automatically (missing fields, enum values,
   etc.). Generated union decoders report invalid discriminators with the allowed
   enum values and report a missing nested union `value` as a missing field, so
   MCP callers receive actionable retry guidance instead of transport-level JSON
   decoder failures. The generated error uses fixed framework text. It does not
   retain the raw Goa validation message or the submitted value.

2. **Execution‑time validation (service / tool provider errors)**

   When a tool provider calls a bound service method, the method may return a structured
   validation error (for example `loom.MissingFieldError`, `loom.InvalidLengthError`, …).
   Providers should surface these as **structured validation issues** in the tool result
   message so consumers can build a `RetryHint` without parsing error strings.
   - **Provider behavior (generated)**: generated providers call
     `toolregistry.ValidationIssues(err)` and, when issues are present, emit an error
     result that includes them.
   - **Wire protocol**: tool result errors may include `issues` (`[]FieldIssue`).
   - **Consumer behavior**: registry executors convert `issues` into a `RetryHint`
     (e.g., `missing_fields`). They do not attach examples or submitted input.

The runtime bounds all generated retry hints to 4096 encoded bytes after final
enrichment. The bound applies to local and registry-routed tool execution.

This keeps the contract strong and deterministic: validation stays at boundaries,
and “what to retry with” is computed from structured data, not heuristics.

---

## Model Middleware

The `features/model/middleware` package provides middleware for model clients.

### Adaptive Rate Limiter

Apply adaptive rate limiting to handle provider throttling:

```go
import mdlmw "github.com/CaliLuke/loom-mcp/v2/features/model/middleware"

rl := mdlmw.NewAdaptiveRateLimiter(
    ctx,
    throughputMap,     // *rmap.Map for cluster-wide state (nil for local)
    "bedrock:sonnet",  // Model family key
    80_000,            // Initial TPM (tokens per minute)
    1_000_000,         // Max TPM
)

limitedClient := rl.Middleware()(rawClient)
rt.RegisterModel("bedrock", limitedClient)
```

The rate limiter adjusts throughput with additive-increase/multiplicative-
decrease (AIMD) when provider calls report rate limits or successful probes. It
does not retry the request or implement time-based exponential backoff.
Capacity reduction and recovery adjust only the bucket's refill rate; the burst
capacity stays pinned at the
configured max TPM, so a request whose estimated cost fits within max TPM always
waits for capacity rather than being rejected after backoffs shrink the budget.
A request estimated above max TPM can never be admitted and fails fast with
`middleware.ErrRequestTooLarge`; raise the limiter's max TPM or reduce the
request size.

`NewAdaptiveRateLimiter` admits requests using estimated input tokens and does
not reserve output tokens. When the wrapped provider implements exact
`model.TokenCounter`, the middleware preserves that optional capability for
callers, but input-only admission still uses the estimator.

Use output reservation when provider quota accounting charges the requested
maximum output as well as the input:

```go
rl := mdlmw.NewOutputReservationAdaptiveRateLimiter(
    ctx,
    throughputMap,
    "bedrock:sonnet",
    80_000,
    1_000_000,
)
limitedClient := rl.Middleware()(rawClient)
```

Output-reservation admission requires the wrapped provider to implement
`model.TokenCounter` with `Exact: true` and each request to set `MaxTokens > 0`.
It reserves `InputTokens + MaxTokens` before calling `Complete` or `Stream`.
The counter can make its own provider request. Missing or inexact counting, an
invalid output limit, integer overflow, or a combined cost above the fixed
maximum burst fails before model generation starts. A rate limit from the
counter triggers the same adaptive backoff as a generation call. The versioned
Pulse key isolates this accounting mode from input-only limiters during rolling
upgrades. When the provider does not implement `model.TokenCounter`, the
wrapped client still does not advertise that optional interface.

---

## Common Patterns

### Bootstrap Helper

Generated `loom example` emits `cmd/<service>/agents_bootstrap.go`:

```go
// Bootstrap creates runtime with Temporal, stores, and registers agents
rt, cleanup, err := bootstrap.New(ctx)
if err != nil {
    log.Fatal(err)
}
defer cleanup()
```

### Pulse Streaming

```go
import pulsestream "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse"

streams, _ := pulsestream.NewRuntimeStreams(pulsestream.RuntimeStreamsOptions{
    Client: pulseClient,
})

rt := runtime.New(
    runtime.WithEngine(eng),
    runtime.WithStream(streams.Sink()),
)

// Subscribe to session events
sub, _ := streams.NewSubscriber(pulsestream.SubscriberOptions{SinkName: "ui"})
events, errs, cancel, _ := sub.Subscribe(ctx, "session/session-123")
defer cancel()

// Consume until you observe `type=="run_stream_end"` for the active run ID.
```

`Subscribe` is the UI/convenience API: it acknowledges each valid entry after
placing it on the local event channel, and acknowledges malformed poison
messages after reporting their decode error. This does not prove that a
downstream database transaction or side effect committed.

Durable consumers use manual deliveries and acknowledge only after committing
their own work:

```go
deliveries, errs, cancel, err := sub.SubscribeManual(ctx, "session/session-123")
if err != nil {
    return err
}
defer cancel()

for delivery := range deliveries {
    if err := delivery.DecodeError(); err != nil {
        if err := deadLetters.Put(ctx, delivery.PulseID(), delivery.RawPayload(), err); err != nil {
            return err // unacked: retry dead-lettering after reclamation
        }
        if err := delivery.Ack(ctx); err != nil {
            return err
        }
        continue
    }
    if err := projection.Apply(ctx, delivery.Event()); err != nil {
        return err // unacked: remains pending for Pulse redelivery/claim
    }
    if err := delivery.Ack(ctx); err != nil {
        return err // retry is safe until Ack succeeds
    }
}
```

Manual mode exposes malformed entries with `DecodeError`, `RawPayload`, and
`PulseID`; it never acknowledges them automatically. Consumers can commit them
to a dead-letter store and then acknowledge, or leave them pending for a
reclamation policy. Pulse delivery is at least once: persist `EventKey()` (or
another stable application key) with the projection to make reprocessing
idempotent; manual acknowledgement alone is not exactly once.

The `errs` channel is best-effort and never blocks event delivery, so draining
it is optional. Decode errors that arrive while its one-slot buffer is full are
dropped and counted (see `Subscriber.DroppedErrors`). Terminal errors (ack
failures) evict any pending decode error so the terminal cause is always
delivered, then both channels close in auto-ack mode — ranging over `events`
alone is enough to observe termination. In manual mode, `Delivery.Ack` returns
ack errors directly and may be retried; they are not sent through `errs`.

### Custom Tool Executor

```go
executor := runtime.ToolCallExecutorFunc(func(ctx context.Context, meta *runtime.ToolCallMeta, call *planner.ToolRequest) (*planner.ToolResult, error) {
    // Access explicit metadata
    log.Printf("Executing %s in run %s, session %s", call.Name, meta.RunID, meta.SessionID)

    // Call your service
    result, err := myService.Execute(ctx, call.Payload)
    if err != nil {
        return nil, err
    }

    return &planner.ToolResult{
        Name:   call.Name,
        Result: result,
    }, nil
})
```

---

## Error Handling

### Sentinel Errors

```go
var (
    ErrAgentNotFound       = errors.New("agent not found")
    ErrEngineNotConfigured = errors.New("runtime engine not configured")
    ErrInvalidConfig       = errors.New("invalid configuration")
    ErrMissingSessionID    = errors.New("session id is required")
    ErrWorkflowStartFailed = errors.New("workflow start failed")
    ErrRegistrationClosed  = errors.New("registration closed after first run")
)
```

### Run Store Errors

```go
var ErrNotFound = errors.New("run not found")  // run.ErrNotFound
```

### Model Errors

```go
var ErrStreamingUnsupported = errors.New("model: streaming not supported")
var ErrRateLimited = errors.New("model: rate limited")
```

---

## Best Practices

1. **Register before running.** All agents and models must be registered before
   the first `Run` or `Start` call. Registration closes afterward.

2. **Use generated clients.** The typed `<agent>.NewClient(rt)` embeds route
   information and provides compile-time safety.

3. **Choose one event owner.** Use a decorated model stream directly or through
   `planner.ConsumeStream`. For another validated stream, give
   `planner.ConsumeStream` the runtime planner-event sink.

4. **Set SessionID for sessionful runs.** `Run` and `Start` require a session ID
   for grouping and memory association. `OneShotRun` is explicitly sessionless.

5. **Trust the contracts.** Don't add defensive checks for values guaranteed by
   generated validation or construction. Let violations fail fast.

6. **Configure stores for production.** In-memory defaults are suitable for
   development; use MongoDB stores for persistence.

7. **Stream events, don't poll.** Use `SubscribeRun` or Pulse subscriptions
   instead of polling run status.

8. **Keep planners focused.** Planners decide what to do (final answer vs. tools).
   Tool implementations handle how.

---

## Glossary

| Term             | Definition                                                                                         |
| ---------------- | -------------------------------------------------------------------------------------------------- |
| **Run**          | A single workflow execution. Has a unique RunID.                                                   |
| **Session**      | Groups related runs (e.g., multi-turn conversation).                                               |
| **Turn**         | A user message → agent response cycle. May span multiple runs if interrupted.                      |
| **Planner**      | Decision-maker that analyzes messages and returns tool calls or final responses.                   |
| **Toolset**      | Collection of related tools with shared execution logic.                                           |
| **Tool Spec**    | Metadata and JSON codecs for a tool (name, schema, codec functions).                               |
| **Bounds**       | Metadata describing how a tool result was truncated or limited.                                    |
| **Engine**       | Workflow backend such as in-memory or Temporal; owns start, signal, query, and durable execution mechanics. |
| **Hook**         | Internal runtime event. Most hooks are durably appended to the runlog before projection.           |
| **Runlog**       | Canonical append-only hook event record for a run.                                                  |
| **Transcript memory** | Derived per-run `memory.Event` projection used for planner history and event search.             |
| **Long-term memory** | Durable `memory.Entry` values managed by `memory.Service`; separate from transcript events.       |
| **Stream Event** | Client-facing, best-effort projection delivered through a runtime stream sink.                     |
| **MCP server**   | Generated protocol surface exposing designed tools, resources, and prompts.                        |
| **MCP caller**   | Runtime client-side adapter that consumes an external or generated MCP server.                     |
| **Tool registry** | Catalog/search/execution infrastructure for tools; distinct from the prompt registry.              |
| **Prompt registry** | Baseline prompt specs plus optional scoped overrides.                                              |
| **MCP skill resource** | `SkillDirectory(...)` projection exposed as `skill://` MCP resources.                             |
| **Model-facing skill tools** | `FromSkills(...)` toolset used by an agent to list and load local instruction packages.          |
| **Finalizer**    | Aggregates child results into parent tool result for agent-as-tool (does not propagate artifacts). |
| **Reminder**     | Structured backstage guidance injected into planner prompts.                                       |
