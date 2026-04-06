# Architecture Review & Refactoring Plan

## 1. Findings (High-Leverage Refactors - Technical Debt)

**ID**: CR-001
**Severity**: S2
**Title**: Monolithic Integration Test Harness Restricts Dev Velocity
**Impact**: The `Runner` implementation acts as a massive bottleneck for development speed and test reliability. Because it mixes fixture preparation, server lifecycle, transport execution (JSON-RPC, SSE, CLI), and assertions into a single 1300+ line file, any change to the test framework risks breaking unrelated transport tests. Furthermore, reliance on fixed sleeps causes test flakiness.
**Evidence**: `integration_tests/framework/runner.go` (1328 lines). Contains raw lifecycle detail alongside `findExampleRoot`, `executeJSONRPC`, and hardcoded `time.Sleep(200 * time.Millisecond)` for startup readiness.
**Recommendation**: 
1. Extract server build, start, stop, and ping behavior into a lifecycle-focused helper.
2. Separate fixture/example patching from transport execution.
3. Replace arbitrary fixed sleeps with explicit polling/readiness checks around `ping`.
**Confidence**: High

**ID**: CR-002
**Severity**: S2
**Title**: Overloaded Registry Manager Violates Single Responsibility Principle
**Impact**: Modifying registry discovery or federation logic carries a high risk of side-effects because one-shot operations and background synchronization share the same control flow blocks. This also makes testing timing-sensitive and difficult.
**Evidence**: `runtime/registry/manager.go` (675 lines). Mixes cache management, tool discovery (`DiscoverToolset`), federation filtering, background synchronization (`StartSync`, `syncRegistry`), and observability logging.
**Recommendation**: 
1. Split discovery and cache paths from background sync and federation logic into separate files/structs.
2. Move observability/logging helpers behind narrower interfaces.
3. Replace test sleeps with deterministic controllable clocks or polling hooks in `manager_test.go`.
**Confidence**: High

**ID**: CR-003
**Severity**: S2
**Title**: High Complexity in Runtime Tool Execution Hotspots
**Impact**: The core loop for agent execution is highly complex, mixing policy, dispatch, validation, and timeout handling. This makes extending agent capabilities or fixing core runtime bugs slow and error-prone.
**Evidence**: `runtime/agent/runtime/tool_calls.go` (884 lines) and `runtime/agent/runtime/agent_tools.go` (718 lines). `executeToolCalls` manages dispatch, timeout synthesis, and result merging all at once.
**Recommendation**: 
1. Split `executeToolCalls` into discrete concern-based helpers: dispatch, result collection, and timeout synthesis.
2. In `agent_tools.go`, separate payload validation and prompt rendering from nested-run request construction and output adaptation.
**Confidence**: High

**ID**: CR-004
**Severity**: S3
**Title**: Duplicated Mongo Client Infrastructure
**Impact**: The four low-level feature clients share boilerplate. If a bug is found in timeout handling, index bootstrapping, or connection handling, it must be fixed in four places, leading to potential drift.
**Evidence**: `features/{memory,prompt,runlog,session}/mongo/clients/mongo/client.go`. All repeat constructor, timeout, ping, and index setup patterns.
**Recommendation**: Extract default timeout resolution, nil-context normalization, and common `Ping` shape into a shared internal helper package, being careful *not* to centralize feature-specific query logic.
**Confidence**: Medium

---

## 2. Architecture Assessment Summary

The overarching architecture of `loom-mcp` is well-reasoned, adhering to a strict "Design-First" philosophy using the underlying Loom generation pipeline. The unified `Toolset` model smoothly treats local, MCP, and Registry tools as first-class citizens, which is an excellent design choice for agent extensibility.

However, the **implementation architecture** has accrued significant monolithic file debt. Several critical files have become catch-all buckets for disparate responsibilities. The lack of boundary enforcement within the runtime and testing packages means that simple feature additions require developers to page massive amounts of context into memory. By prioritizing the decomposition of these hotspots, the framework will drastically improve its maintainability and speed of development without altering external behavior.

---

## 3. Test/Verification Gaps

- **Timing Debt in Tests**: Widespread use of `time.Sleep()` instead of deterministic waiting or polling in `integration_tests/framework/runner.go` and `runtime/registry/manager_test.go`. This masks true readiness state and causes intermittent CI failures.
- **Missing Characterization Tests**: Before refactoring the hotspots, existing behavior (especially around server startup/shutdown and registry cache hit/miss semantics) lacks isolated characterization tests to guarantee safe extraction.

---

## 4. Prioritized Remediation Plan

To gain the highest leverage and immediate speed-of-development improvements, refactoring should be executed in small, verifiable batches in the following strict order:

1. **Phase 1: Integration Harness (Highest Leverage)**
   - Extract `startServer`, `stopServer`, and `ping` from `Runner`.
   - Separate fixture prep from transport logic (JSON-RPC vs SSE).
   - Replace fixed startup sleeps with deterministic readiness checks.
2. **Phase 2: Registry Manager**
   - Separate one-shot tool discovery (`DiscoverToolset`) from background federation sync loops.
   - Decouple observability from core business decisions.
3. **Phase 3: Runtime Orchestration**
   - Refactor `runtime/agent/runtime/tool_calls.go` to split dispatch, merge, and timeout logic.
   - Refactor `runtime/agent/runtime/agent_tools.go` to split validation from execution.
4. **Phase 4: Mongo Client Consolidation**
   - Inventory parity across memory, prompt, runlog, and session stores.
   - Extract common connection and timeout infrastructure.

---

## 5. Fundamental Architectural Deep Dive

Beyond the known refactoring hotspots, an investigation into the core design principles of `loom-mcp` reveals several deep architectural insights regarding the DSL, Codegen, Runtime, and Streaming models.

### 5.1 The Streaming Model & `RunCompletedEvent`
- **Integration**: The SSE streaming stack gracefully avoids reinventing serialization; instead, it elegantly piggybacks on Goa's generated JSON-RPC layer. `DESIGN.md` explicitly states: "When your methods stream, the JSON-RPC generator emits the SSE stack." 
- **The Event Boundary**: The workflow runtime emits a terminal lifecycle event (`hooks.RunCompletedEvent`), which carries `status`, `phase`, and a deterministic `public_error`. The stream subscriber (`runtime/agent/stream/subscriber.go`) translates this internal event into a `workflow` SSE payload. 
- **Coupling/Buffering**: While the boundary is conceptually clean, it requires a long chain of translation (Temporal Workflow -> Hook Activity -> Event Bus -> Subscriber -> SSE Pipeline). The runtime is tightly coupled to publishing these hook events synchronously (`r.publishHookErr`). A failure in the event bus or storage sink forces the core agent execution loop to fail. This is a risk surface for operability.

### 5.2 Policy Evaluation Boundary
- **Implementation**: The framework provides a policy engine (`features/policy/basic/engine.go`) implementing the `policy.Engine` interface to enforce `AllowTags`, `BlockTags`, and `RetryHints`. The DSL defines these via `RunPolicy` and `DefaultCaps`.
- **Evaluation Timing**: The engine evaluates policies via its `Decide` method taking `policy.Input`. This operates *before* tool dispatch, acting as a clean interceptor. 
- **Architectural Leak**: However, because the engine is invoked in the middle of the tool execution loop, the actual orchestration logic (`runtime/agent/runtime/tool_calls.go`) must manually interpret the `policy.Decision` (e.g., merging limits and applying allowed tool sets). This forces the core runtime dispatcher to understand policy nuance rather than receiving a pre-filtered, transparent list of capabilities. 

### 5.3 Agent Memory, Session, and State Injection
- **Abstraction**: Memory (`runlog.Store`) and Session (`session.Store`) are abstracted behind clean interfaces (e.g., `UpsertRun`, `LoadSession`).
- **Injection**: These stores are injected into the runtime engine via functional options (`WithSessionStore`, `WithRunEventStore`).
- **Pollution vs. Purity**: The injection is interface-driven, which is positive. However, the core Temporal workflow logic in `runtime/agent/runtime` is responsible for actively emitting the state changes (via hook publishing) rather than state updates being an independent observer. This means the core execution loops are slightly polluted with lifecycle management logic (`r.updateRunStatus(ctx, e.RunID(), session.RunStatusCompleted)`).

### 5.4 The DSL / Codegen / Runtime Unification (Leaky Abstractions)
- **Unification**: All toolset providers (Local, MCP, Registry) unify down to a single type-erased closure in `ToolsetRegistration`: `Execute func(ctx, call) (*ToolResult, error)`. This is a powerful, extensible design that makes the runtime largely unaware of how tools actually run.
- **Leaks**: The `ToolsetRegistration` struct has begun to leak execution semantics. It includes flags like `Inline bool` and `AgentTool *AgentToolConfig`. These exist so the workflow runtime knows *not* to run an agent-as-a-tool as a standard Activity, but instead to spawn a nested Temporal Child Workflow. This breaks the "black box" abstraction of the `Execute` closure, forcing the runtime dispatcher to inspect the registration metadata and alter its orchestration path based on the provider type.