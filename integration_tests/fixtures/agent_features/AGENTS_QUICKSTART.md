# Welcome to Your loom-mcp Agents! 👋

This guide is your personal co-pilot, generated specifically to help you bring your new AI agents to life. We'll go from the code Goa just created to a running agent in a few simple steps.

> **A Quick Note on This File:**
>
> - **Want to hide me?** No problem! Add `DisableAgentDocs()` to your `API` design and I won't be generated next time.
> - **Safety First:** It's safe to delete this file. It will reappear, updated, after the next `loom gen`.
> - **Golden Rule:** Never edit the `gen/` directory directly. Your design files are the source of truth!

---

## 1. Your Design, At a Glance ✨

Here’s a map of what loom-mcp just built for you based on your `design/*.go` files.
* **Service `features`:**
    * **Agent `coordinator`** (ID: `features.coordinator`):
        * **Mission:** *Generated acceptance agent*
        * **Uses Toolsets:**
            * `features.artifacts`
            * `features.long_term_memory`
            * `features.memory`
            * `features.skills`
            * `features.workflow`
        * **Exports Toolsets:***none*
        * **Run Policy:**
            * Max Tool Calls: `12`
            * Max Consecutive Failures: `2`
            * Time Budget: `30s`
            * Interrupts Allowed: `false`
    * **Agent `registry-validator`** (ID: `features.registry_validator`):
        * **Mission:** *Generated registry schema validation fixture*
        * **Uses Toolsets:**
            * `features.registry_validation`
        * **Exports Toolsets:***none*
        * **Run Policy:**
            * Max Tool Calls: `0`
            * Max Consecutive Failures: `0`
            * Time Budget: `0s`
            * Interrupts Allowed: `false`

---

## 2. 🚀 The 3-Step Liftoff: Your First Agent Run

The fastest way to run your agent is using the generated example scaffolding.

### Quick Start (Recommended)

```bash
# 1. Generate code and example files
loom gen <module>/design
loom example <module>/design

# 2. Run the generated example
go run ./cmd/<service>/
```

This generates:
- `internal/agents/bootstrap/bootstrap.go` — Wires runtime and registers agents
- `internal/agents/<agent>/planner/planner.go` — Stub planner (edit to connect your LLM)
- `cmd/<service>/main.go` — Example main that uses the bootstrap

### Understanding the Generated Code

The generated `cmd/<service>/main.go` uses the bootstrap to run your agents. Here's what it does under the hood:

```go
package main

import (
    "context"
    "fmt"

    // The core loom-mcp runtime and planner interfaces
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"

    // === Your Generated Agent Packages ===
    // (Goa generated these based on your design)
    coordinator "example.com/agentfeatures/gen/features/agents/coordinator"
    registry_validator "example.com/agentfeatures/gen/features/agents/registry_validator"
)

// A simple "brain" for our agent. It just says hello for now.
// We'll make this smarter in the next section!
type StubPlanner struct{}
func (p *StubPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
    return &planner.PlanResult{
		FinalResponse: &planner.FinalResponse{
			Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "Hello!"}},
			},
		},
	}, nil
}
func (p *StubPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
    return &planner.PlanResult{
		FinalResponse: &planner.FinalResponse{
			Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "Done."}},
			},
		},
	}, nil
}

func main() {
    // 1. Create the Runtime
    // This is the central engine for all your agents.
    rt := runtime.New()

    // 2. Register Your Agent(s)
    // Let the runtime know about the agents it can manage.
    {
        cfg := coordinator.CoordinatorAgentConfig{
            Planner: &StubPlanner{},
            // We'll add tool configurations here later on.
        }
        if err := coordinator.RegisterCoordinatorAgent(context.Background(), rt, cfg); err != nil {
            panic(err)
        }
    }
    {
        cfg := registry_validator.RegistryValidatorAgentConfig{
            Planner: &StubPlanner{},
            // We'll add tool configurations here later on.
        }
        if err := registry_validator.RegisterRegistryValidatorAgent(context.Background(), rt, cfg); err != nil {
            panic(err)
        }
    }

    // 3. Run it!
    // Let's invoke our first agent and see what it says using AgentClient.
    fmt.Println("🚀 Invoking agent...")
    if _, err := rt.CreateSession(context.Background(), "my-first-session"); err != nil {
        panic(err)
    }
    client := coordinator.NewClient(rt)
    out, err := client.Run(
        context.Background(),
        "my-first-session",
        []*model.Message{
			{ Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "Hi there!"}} },
		},
    )
    if err != nil {
		panic(err)
	}

    fmt.Println("✅ Success!")
    fmt.Println("RunID:", out.RunID)
    // Print first text part (if any)
    if out.Final != nil && len(out.Final.Parts) > 0 {
        if tp, ok := out.Final.Parts[0].(model.TextPart); ok {
            fmt.Println("Assistant says:", tp.Text)
        }
    }
}
```

---

## 3. Meet Your Agents 🤖

Here are the detailed cheat sheets for each agent you designed.
<details>
<summary><strong>Agent: <code>coordinator</code></strong> (ID: <code>features.coordinator</code>)</summary>

* **Package:** `example.com/agentfeatures/gen/features/agents/coordinator`
* **Directory:** `gen/features/agents/coordinator`
* **Config Struct:** `CoordinatorAgentConfig`
* **Register Function:** `RegisterCoordinatorAgent(ctx, rt, cfg)`
* **How to Run:**
    * **Sessions are first-class:** call `rt.CreateSession(ctx, sessionID)` once before you start any runs under that session ID.
    * **Synchronous (wait for result):**
        ```go
        client := coordinator.NewClient(rt)
        out, err := client.Run(ctx, sessionID, messages)
        ```
    * **Asynchronous (get a handle):**
        ```go
        client := coordinator.NewClient(rt)
        handle, err := client.Start(ctx, sessionID, messages)
        ```
* **Workflow Name:** `features.coordinator.workflow` (Queue: `features_coordinator_workflow`)

#### Minimal Configuration```go
cfg := coordinator.CoordinatorAgentConfig{
    Planner: myPlanner,
}
```
</details>
<details>
<summary><strong>Agent: <code>registry-validator</code></strong> (ID: <code>features.registry_validator</code>)</summary>

* **Package:** `example.com/agentfeatures/gen/features/agents/registry_validator`
* **Directory:** `gen/features/agents/registry_validator`
* **Config Struct:** `RegistryValidatorAgentConfig`
* **Register Function:** `RegisterRegistryValidatorAgent(ctx, rt, cfg)`
* **How to Run:**
    * **Sessions are first-class:** call `rt.CreateSession(ctx, sessionID)` once before you start any runs under that session ID.
    * **Synchronous (wait for result):**
        ```go
        client := registry_validator.NewClient(rt)
        out, err := client.Run(ctx, sessionID, messages)
        ```
    * **Asynchronous (get a handle):**
        ```go
        client := registry_validator.NewClient(rt)
        handle, err := client.Start(ctx, sessionID, messages)
        ```
* **Workflow Name:** `features.registry_validator.workflow` (Queue: `features_registry_validator_workflow`)

#### Minimal Configuration```go
cfg := registry_validator.RegistryValidatorAgentConfig{
    Planner: myPlanner,
}
```
</details>

---

## 4. 🧠 The Planner: Giving Your Agent a Brain

The `Planner` is where your agent's intelligence lives. It connects to an LLM to decide what to do next. The `StubPlanner` above is great for testing, but here's the correct interface for a real implementation.

```go
type MySmartPlanner struct{}

// PlanStart is called at the beginning of a run.
func (p *MySmartPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
    // 1. Get an LLM client from the runtime.
    // mc, _ := in.Agent.ModelClient("bedrock")
    
    // 2. Build a prompt from in.Messages.
    
    // 3. Call the LLM and decide whether to call tools or give a final answer.
    return &planner.PlanResult{
        FinalResponse: &planner.FinalResponse{
            Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "I'm ready to help!"}},
			},
        },
    }, nil
}

// PlanResume is called after tools have run, giving the agent new information.
func (p *MySmartPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
    // 1. Inspect the tool results from in.ToolResults.
    // 2. Build a new prompt including the tool results.
    // 3. Call the LLM to decide what to do next.
    return &planner.PlanResult{
        FinalResponse: &planner.FinalResponse{
            Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "The tools have run. Here's what I found..."}},
			},
        },
    }, nil
}
```

---

## 5. 🛠️ Giving Your Agents Tools

Your agents can do useful work by calling other parts of your system. Here's how to wire them up.

#### Local Service-Backed Tools (`BindTo`) — Executor-First

When your tool maps to a service method (via `BindTo`):
- `loom gen` emits typed tool specs/codecs under the owner-scoped `gen/<service>/toolsets/<toolset>/` package
- `loom gen` emits transform helpers in that package's `transforms.go` when the shapes are compatible
- `loom example` emits an application-owned executor stub under `internal/agents/<agent>/toolsets/<toolset>/execute.go`

Wire executors using the generated `RegisterUsedToolsets` helper:

```go
// After registering the agent, wire the toolset executors
err := <agentpkg>.RegisterUsedToolsets(ctx, rt,
    <agentpkg>.With<ToolsetName>Executor(
        runtime.ToolCallExecutorFunc(<toolsetpkg>.Execute),
    ),
)
if err != nil { panic(err) }
```

Implement the executor's `Execute` function to:
- Switch on `call.Name` for each tool
- Use the generated tool constant; canonical IDs have the form `<toolset>.<tool>` (without the service name)
- Decode `call.Payload` to typed args using the generated codec
- Optionally use `Init<Tool>MethodPayload` / `Init<Tool>ToolResult` transforms
- Call your service client and return a `planner.ToolResult`

Minimal executor scaffold:

```go
// internal/agents/<agent>/toolsets/<toolset>/execute.go
package <toolset>

import (
    "context"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
    specs "<module>/gen/<service>/toolsets/<toolset>"
)

func Execute(ctx context.Context, meta *runtime.ToolCallMeta, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
    switch call.Name {
    case specs.<Tool>:
        // Decode payload using generated codec
        pc, ok := specs.PayloadCodec(string(call.Name))
        if !ok {
            return runtime.Executed(&planner.ToolResult{Error: planner.NewToolError("payload codec not found")}), nil
        }
        args, err := pc.FromJSON(call.Payload)
        if err != nil {
            return runtime.Executed(&planner.ToolResult{Error: planner.NewToolError("invalid payload: " + err.Error())}), nil
        }
        // Type-assert to the generated payload type:
        // typedArgs := args.(*specs.<ToolPayload>)
        // Optionally transform it: methodPayload := specs.Init<Tool>MethodPayload(typedArgs)
        // Call your service client, then map its result:
        // toolResult := specs.Init<Tool>ToolResult(methodResult)
        // Or build a typed tool result directly:
        // toolResult := &specs.<ToolResult>{Status: "ok"}
        return runtime.Executed(&planner.ToolResult{
			Name:   call.Name,
			Result: &specs.<ToolResult>{
				Status: "ok",
			},
		}), nil
    }
    return runtime.Executed(&planner.ToolResult{
		Error: planner.NewToolError("unknown tool"),
	}), nil
}
```
---

#### Service-Side Tool Providers (Registry-Routed Execution)

When a toolset is **method-backed** (a tool is declared via `BindTo(...)`) and the toolset is owned by a service, loom-mcp also generates a **tool provider**:

- `gen/<service>/toolsets/<toolset>/provider.go`

The provider implements `HandleToolCall(ctx, msg)` which:

- Decodes the incoming tool payload JSON using the generated payload codec
- Builds the Goa method payload (using the generated transforms)
- Calls the bound service method
- Encodes the tool result JSON (and optional artifact/sidecar) using the generated result codec

To serve tool calls from the registry gateway, run the provider loop inside the owning service process:

```go
// cmd/<service>/main.go (or your service bootstrap)
handler := <toolsetpkg>.NewProvider(svcImpl)
go func() {
    err := toolprovider.Serve(ctx, pulseClient, toolsetID, handler, toolprovider.Options{
        Pong: func(ctx context.Context, pingID string) error {
            return registryClient.Pong(ctx, &registry.PongPayload{
                PingID:  pingID,
                Toolset: toolsetID,
            })
        },
    })
    if err != nil {
        panic(err)
    }
}()
```

Notes:

- The registry publishes tool calls to the deterministic stream `toolset:<toolsetID>:requests` and providers publish results to `result:<toolUseID>`.
- Providers are generated only when the toolset has at least one **method-backed** tool (and the toolset is not registry-backed).

#### Connecting to Remote Services (MCP)

If your agent uses a top-level MCP-backed toolset declared with
`Toolset(FromMCP(...))` and referenced with `Use(...)`:

1.  Get the generated JSON-RPC MCP client for the remote service.
2.  Wrap it with that generated client's `NewCaller(client, suite)` helper.
3.  Pass it to your agent's config, using the generated constant for the key.

```go
// 1. Get the generated JSON-RPC MCP client for the remote service.
remoteClient := <jsonrpc_client_pkg>.NewClient(/* your endpoints */)

// 2. Wrap it in the generated MCP Caller adapter.
caller := <jsonrpc_client_pkg>.NewCaller(remoteClient, "<mcp-suite>")

// 3. Supply it in the agent config.
cfg := <agentpkg>.<AgentConfig>{
    Planner: myPlanner,
    MCPCallers: map[string]mcpruntime.Caller{
        <agentpkg>.<ToolsetIDConst>: caller, // e.g., "assistant.assistant-mcp"
    },
}
```

---
<details>
<summary><strong>Click to see a detailed reference of your agent's toolboxes...</strong></summary>

## 6. Your Agent's Toolbox: A Reference

### Agent `coordinator` Toolsets

* **Tools this agent can USE:**
* **`features.artifacts`**
* **`features.long_term_memory`**
* **`features.memory`**
* **`features.skills`**
* **`features.workflow`**
* **Tool: `workflow.draft`**
* *Draft a response*
* **Tool: `workflow.method_echo`**
* *Echo a topic through the generated method dispatcher*
* **Tool: `workflow.publish`**
* *Publish the result*
* **Tool: `workflow.retry`**
* *Run a bounded retry step*
* **Tool: `workflow.review`**
* *Review the draft*
* **Tool: `workflow.revise`**
* *Revise the result*
* **Tools this agent EXPORTS for others to use:**
* *This agent does not export any toolsets.*

### Agent `registry-validator` Toolsets

* **Tools this agent can USE:**
* **`features.registry_validation`**
* **Tools this agent EXPORTS for others to use:**
* *This agent does not export any toolsets.*
</details>

---

## 7. Agents Calling Agents (The `Exports` Keyword)

When an agent `Exports` a toolset, other agents can call it. loom-mcp generates a special `agenttools` package to make this easy.

```go
// In your main.go, register the exported toolset so others can find it.
reg, err := <agenttools>.NewRegistration(
    rt,
    "You are a helpful specialist assistant.",  // A system prompt for the nested agent (optional)
    // Configure per-tool content (optional). If omitted, the runtime builds a default
    // user message from the payload; override the builder with WithPromptBuilder.
    runtime.WithText(<agenttools>.ToolXYZ, "Please perform the following task: {{ . }}"),
)
if err != nil { panic(err) }

// Now this toolset is available in the runtime for other agents to use!
if err := rt.RegisterToolset(reg); err != nil { panic(err) }
```

---

## 8. Ready for Prime Time: Advanced Features 🔭

* **Sessions & Runs:** Sessions are explicit. Create them with `rt.CreateSession(ctx, sessionID)` and end them with `rt.DeleteSession(ctx, sessionID)`. Runs (`client.Run`/`client.Start`) require an active session.
* **Session-Owned Streaming (for UIs):** In production, stream consumers should attach to the **session-owned stream** (`session/<session_id>`) and filter by `run_id`. Close SSE when you observe a `run_stream_end` event for the attached run ID. Nested agent runs emit `child_run_linked` links and their own `run_stream_end`; parent runs only emit `run_stream_end` after all child runs have ended.
* **Asynchronous Runs:** Use `client.Start()` to get a workflow handle. This is great for long-running tasks, cancellation, and non-interactive integrations.
* **Interrupts (Human-in-the-Loop):** If your policy allows it, you can pause and resume agent runs with `rt.PauseRun()` and `rt.ResumeRun()`.
* **Policies & Caps:** The `RunPolicy` in your design (max tool calls, time budgets) is automatically enforced by the runtime.
* **Persistence & Observability:** The `runtime.New` function accepts functional options such as `runtime.WithEngine(...)`, `runtime.WithMemoryStore(...)`, `runtime.WithStream(...)`, and `runtime.WithHooks(...)` to configure production-grade components.
* **Temporal DataConverter (required):** When you use the Temporal engine, configure the Temporal client with `temporal.NewAgentDataConverter(...)` to enforce goa‑ai's boundary contract: tool results and artifacts cross workflow boundaries as canonical JSON bytes (`api.ToolEvent` / `api.ToolArtifact`), and `planner.ToolResult` is rejected if it ever tries to cross a Temporal boundary.
* **Registries & Discovery:** When you declare registries and `FromRegistry(...)` toolsets in your DSL, loom-mcp generates typed registry HTTP clients under `gen/<svc>/registry/<name>/` plus per-toolset specs helpers (with `DiscoverAndPopulate`, `Specs`, and `RegistryToolsetID`) so you can discover tools at runtime and register executors using `runtime.ToolsetRegistration`.

```go
// Example of production-ready runtime options
rt := runtime.New(
    runtime.WithEngine(myTemporalEngine),
    runtime.WithMemoryStore(myMongoMemoryStore),
    runtime.WithStream(myEventStreamSink),
)
```

Example: constructing a Temporal engine with the required DataConverter:

```go
import (
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
    "go.temporal.io/sdk/client"

    // Your generated tool specs aggregate.
    // The generated package exposes: func Spec(tools.Ident) (*tools.ToolSpec, bool)
    specs "<module>/gen/<service>/agents/<agent>/specs"
)

eng, err := temporal.NewWorker(temporal.Options{
    ClientOptions: &client.Options{
        HostPort:      "127.0.0.1:7233",
        Namespace:     "default",
        // Required: enforce loom-mcp's workflow boundary contract.
        // Tool results/artifacts cross boundaries as canonical JSON bytes (api.ToolEvent/api.ToolArtifact).
        DataConverter: temporal.NewAgentDataConverter(specs.Spec),
    },
    WorkerOptions: temporal.WorkerOptions{
        TaskQueue: "<service>_<agent>_workflow",
    },
})
if err != nil {
    panic(err)
}
defer eng.Close()

// In caller-only processes, use temporal.NewClient(...) with the same ClientOptions
// and pass it to runtime.New(runtime.WithEngine(eng)).
```

---

## 9. 📜 The Golden Rules: Working with Codegen

* ✍️ **Design First:** Always make changes in your `design/*.go` files.
* 🔄 **Regenerate:** Run `loom gen <module>/design` to apply your changes.
* 🚫 **Hands Off `gen/`:** Never edit the `gen/` directory by hand. Your changes will be overwritten!

---

## 10. 🤔 Stuck? Common Questions & Fixes

* **Error: "runtime not initialized"**
* **Fix:** Ensure you register agents with the same runtime instance you use to start runs.
* **Error: "agent not registered"**
    * **Fix:** Check that `Register<AgentName>(...)` was called successfully for that agent before you tried to run it.
* **Error: "session id is required"**
    * **Fix:** Always provide a unique, non-empty string for the `sessionID` when calling `agent.Run(...)`.
* **Error: "session not found"**
    * **Fix:** Sessions are explicit—call `rt.CreateSession(ctx, sessionID)` once before starting runs under that session ID.
* **Error: "mcp caller is required for <suite>"**
    * **Fix:** Your agent's config is missing an entry in the `MCPCallers` map for the specified toolset ID. See section 5.
* **Agent-as-Tool isn't working?**
    * **Fix:** Ensure you've provided `WithText` or `WithTemplate` for **every single tool** in the exported toolset when calling `NewRegistration`.
