// Package runtime implements the core orchestration engine for goa-ai agents.
// It coordinates workflow execution, planner invocations, tool scheduling, policy
// enforcement, memory persistence, and event streaming. The Runtime instance serves
// as the central registry for agents, toolsets, models, and manages their lifecycle
// through durable workflow execution (typically via Temporal).
//
// Key responsibilities:
//   - Agent and toolset registration with validation
//   - Workflow lifecycle management (start, execute, resume)
//   - Policy enforcement (caps, timeouts, tool filtering)
//   - Memory persistence via hook subscriptions
//   - Event streaming and telemetry integration
//   - Tool execution and JSON codec management
//
// The Runtime is thread-safe and can be used concurrently to register agents
// and execute workflows. Production deployments typically configure the Runtime
// with a durable workflow engine (Temporal) and a durable memory store.
//
// Example usage: use AgentClient for execution.
//
//	rt := runtime.New(runtime.Options{ Engine: temporalEngine, ... })
//	if err := rt.RegisterAgent(ctx, agentReg); err != nil {
//		log.Fatal(err)
//	}
//	client := rt.MustClient(agent.Ident("service.agent"))
//	out, err := client.Run(ctx, "s1", messages)
package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	agent "goa.design/goa-ai/runtime/agent"
	"goa.design/goa-ai/runtime/agent/engine"
	"goa.design/goa-ai/runtime/agent/hooks"
	"goa.design/goa-ai/runtime/agent/memory"
	"goa.design/goa-ai/runtime/agent/model"
	"goa.design/goa-ai/runtime/agent/planner"
	"goa.design/goa-ai/runtime/agent/policy"
	"goa.design/goa-ai/runtime/agent/prompt"
	"goa.design/goa-ai/runtime/agent/reminder"
	"goa.design/goa-ai/runtime/agent/runlog"
	"goa.design/goa-ai/runtime/agent/session"
	"goa.design/goa-ai/runtime/agent/stream"
	"goa.design/goa-ai/runtime/agent/telemetry"
	"goa.design/goa-ai/runtime/agent/tools"

	"text/template"
)

type (
	// HintOverrideFunc can override the call hint for a tool invocation.
	//
	// Contract:
	//   - Returning (hint, true) selects hint as the DisplayHint, even when a DSL
	//     template exists.
	//   - Returning ("", false) indicates no override applies and the runtime should
	//     use its default behavior.
	//   - The payload value is the typed payload decoded via the tool payload codec
	//     when possible; it may be nil when decoding fails.
	HintOverrideFunc func(ctx context.Context, tool tools.Ident, payload any) (hint string, ok bool)

	// Runtime orchestrates agent workflows, policy enforcement, memory persistence,
	// and event streaming. It serves as the central registry for agents, toolsets,
	// and models. All public methods are thread-safe and can be called concurrently.
	//
	// The Runtime coordinates with several subsystems:
	//   - Workflow engine (Temporal) for durable execution
	//   - Policy engine for runtime caps and tool filtering
	//   - Memory store for transcript persistence
	//   - Event bus (hooks) for observability and streaming
	//   - Telemetry subsystems (logging, metrics, tracing)
	//
	// Lifecycle:
	//  1. Construct with New()
	//  2. Register agents, toolsets, and models
	//  3. Start workflows via AgentClient (Run or Start)
	//
	// The Runtime automatically subscribes to hooks for memory persistence and
	// stream publishing when MemoryStore or Stream are configured.
	Runtime struct {
		// Engine is the workflow backend adapter (Temporal by default).
		Engine engine.Engine
		// MemoryStore persists run transcripts and annotations.
		Memory memory.Store
		// PromptRegistry resolves prompt specs and optional scoped overrides.
		PromptRegistry *prompt.Registry
		// SessionStore persists session lifecycle state and run metadata.
		SessionStore session.Store
		// Policy evaluates allowlists and caps per planner turn.
		Policy policy.Engine
		// RunEventStore is the canonical append-only run event log.
		RunEventStore runlog.Store
		// Bus is the bus used for streaming runtime events.
		Bus hooks.Bus
		// Stream publishes planner/tool/assistant events to the caller.
		Stream stream.Sink
		// streamSubscriber forwards hook events to Stream. It is invoked from
		// hookActivity so stream emission can be made fatal while a session is active.
		streamSubscriber *stream.Subscriber

		logger  telemetry.Logger
		metrics telemetry.Metrics
		tracer  telemetry.Tracer

		mu        sync.RWMutex
		agents    map[agent.Ident]AgentRegistration
		toolsets  map[string]ToolsetRegistration
		toolSpecs map[tools.Ident]tools.ToolSpec
		// parsed tool payload schemas cached by tool name for hint building
		toolSchemas map[string]map[string]any
		models      map[string]model.Client

		// Per-agent tool specs registered during agent registration for introspection.
		agentToolSpecs map[agent.Ident][]tools.ToolSpec

		handleMu   sync.RWMutex
		runHandles map[string]engine.WorkflowHandle

		// completionRepairMu serializes no-handle terminal repair so concurrent
		// readers cannot append duplicate RunCompleted events for the same missing
		// canonical completion window.
		completionRepairMu sync.Mutex

		// workers holds optional per-agent worker configuration supplied at
		// construction time.
		workers map[agent.Ident]WorkerConfig

		// registrationClosed prevents late agent/toolset registration after the
		// runtime has been explicitly sealed or the first run has been submitted,
		// avoiding dynamic handler registration on active workers.
		registrationClosed bool

		// hookActivityRegistered tracks whether the runtime hook activity has
		// been registered with the engine.
		hookActivityRegistered bool

		// hookActivityTimeout overrides the StartToClose timeout used for the
		// hook publishing activity (`runtime.publish_hook`). Zero means use the
		// runtime default.
		hookActivityTimeout time.Duration

		// reminders manages run-scoped system reminders used for backstage
		// guidance (safety, correctness, workflow) injected into prompts by
		// planners. It is internal to the runtime; planners interact with it
		// via PlannerContext.
		reminders *reminder.Engine

		// toolConfirmation configures runtime-enforced confirmation for selected tools.
		// It is used to require explicit operator approval before executing certain tools.
		// See ToolConfirmationConfig for details.
		toolConfirmation *ToolConfirmationConfig

		hintOverrides map[tools.Ident]HintOverrideFunc
	}

	// Options configures the Runtime instance. All fields are optional except Engine
	// for production deployments. Noop implementations are substituted for nil Logger,
	// Metrics, and Tracer. A default in-memory event bus is created if Hooks is nil.
	Options struct {
		// Engine is the workflow backend adapter (Temporal by default).
		Engine engine.Engine
		// MemoryStore persists run transcripts and annotations.
		MemoryStore memory.Store
		// PromptStore resolves scoped prompt overrides. When nil, prompt rendering
		// uses baseline registered PromptSpecs only.
		PromptStore prompt.Store
		// SessionStore persists session lifecycle state and run metadata.
		SessionStore session.Store
		// Policy evaluates allowlists and caps per planner turn.
		Policy policy.Engine
		// RunEventStore is the canonical append-only run event log.
		RunEventStore runlog.Store
		// Hooks is the Pulse-backed bus used for streaming runtime events.
		Hooks hooks.Bus
		// Stream publishes planner/tool/assistant events to the caller.
		Stream stream.Sink
		// Logger emits structured logs (usually backed by Clue).
		Logger telemetry.Logger
		// Metrics records counters/histograms for runtime operations.
		Metrics telemetry.Metrics
		// Tracer emits spans for planner/tool execution.
		Tracer telemetry.Tracer

		// HookActivityTimeout overrides the StartToClose timeout for the
		// hook publishing activity (`runtime.publish_hook`). Zero means use the
		// runtime default.
		HookActivityTimeout time.Duration

		// Workers provides per-agent worker configuration. If an agent lacks
		// an entry, the runtime uses a default worker configuration. Engines
		// that do not poll (in-memory) ignore this map.
		Workers map[agent.Ident]WorkerConfig

		// ToolConfirmation configures runtime-enforced confirmation overrides for selected
		// tools (for example, requiring explicit operator approval before executing
		// additional tools that are not marked with design-time Confirmation).
		ToolConfirmation *ToolConfirmationConfig

		// HintOverrides optionally overrides DSL-authored call hints for specific tools
		// when streaming tool_start events.
		HintOverrides map[tools.Ident]HintOverrideFunc
	}

	// RuntimeOption configures the runtime via functional options passed to NewWith.
	RuntimeOption func(*Options)

	// WorkerConfig configures per-agent queue placement. Engines that support
	// background workers (for example Temporal) use this to select the workflow
	// and activity queue for the agent. Engine-specific concurrency, liveness,
	// and queue-wait tuning belongs in the engine adapter. In-memory engines
	// ignore this configuration.
	WorkerConfig struct {
		// Queue overrides the default task queue for this agent's workflow and
		// activities. When set, the runtime rebases workflow, planner, and tool
		// activities onto this queue. Engine-specific liveness and queue-wait tuning
		// belongs in the engine adapter, not the generic runtime surface.
		Queue string
	}

	// WorkerOption configures a WorkerConfig.
	WorkerOption func(*WorkerConfig)

	// AgentRegistration bundles the generated assets for an agent. This struct is
	// produced by codegen and passed to RegisterAgent to make an agent available
	// for execution.
	AgentRegistration struct {
		// ID is the unique agent identifier (service.agent).
		ID agent.Ident
		// Planner is the concrete planner implementation for the agent.
		Planner planner.Planner
		// Workflow describes the durable workflow registered with the engine.
		Workflow engine.WorkflowDefinition
		// Toolsets enumerates tool registrations exposed by this agent package.
		Toolsets []ToolsetRegistration
		// PlanActivityName names the activity used for PlanStart.
		PlanActivityName string
		// PlanActivityOptions describes retry/timeout behavior for the PlanStart activity.
		PlanActivityOptions engine.ActivityOptions
		// ResumeActivityName names the activity used for PlanResume.
		ResumeActivityName string
		// ResumeActivityOptions describes retry/timeout behavior for the PlanResume activity.
		ResumeActivityOptions engine.ActivityOptions
		// ExecuteToolActivity is the logical name of the registered ExecuteTool activity.
		ExecuteToolActivity string
		// ExecuteToolActivityOptions describes retry/timeout/queue for the ExecuteTool activity.
		// When set, these options are applied to all service-backed tool activities
		// scheduled by this agent. Agent-as-tool executions run as child workflows.
		ExecuteToolActivityOptions engine.ActivityOptions
		// Specs provides JSON codecs for every tool declared in the agent design.
		Specs []tools.ToolSpec
		// Policy configures caps/time budget/interrupt settings for the agent.
		Policy RunPolicy
	}

	// ToolsetRegistration holds the metadata and execution logic for a toolset.
	// Users register toolsets by providing an Execute function that handles all
	// tools in the toolset. Codegen auto-generates registrations for service-based
	// tools and agent-tools; users provide registrations for custom/server-side tools.
	//
	// The Execute function is the core dispatch mechanism for toolsets that run
	// inside activities or other non-workflow contexts. For inline toolsets, the
	// runtime may invoke Execute directly from the workflow loop.
	ToolsetRegistration struct {
		// Name is the qualified toolset name (e.g., "service.toolset_name").
		Name string

		// Description provides human-readable context for tooling.
		Description string

		// Metadata captures structured policy metadata about the toolset.
		Metadata policy.ToolMetadata

		// Execute invokes the concrete tool implementation for a given tool call.
		// Returns a ToolResult containing the payload, telemetry, errors, and retry hints.
		//
		// For service-based tools, codegen generates this function to call service clients.
		// For agent-tools (Exports), generated registrations set Inline=true and
		// populate AgentTool so the workflow runtime can start nested agents as child
		// workflows and adapt their RunOutput into a ToolResult.
		// For custom/server-side tools, users provide their own implementation.
		Execute func(ctx context.Context, call *planner.ToolRequest) (*planner.ToolResult, error)

		// Specs enumerates the codecs associated with each tool in the set.
		// Used by the runtime for JSON marshaling/unmarshaling and schema validation.
		Specs []tools.ToolSpec

		// TaskQueue optionally overrides the queue used when scheduling this toolset's activities.
		TaskQueue string

		// Inline indicates that tools in this toolset execute inside the workflow
		// context (not as activities). For agent-as-tool, the executor needs a
		// WorkflowContext to start the provider as a child workflow. Service-backed
		// toolsets should leave this false so calls run as activities (isolation/retries).
		Inline bool

		// CallHints optionally provides precompiled templates for call display hints
		// keyed by tool ident. When present, RegisterToolset installs these in the
		// global hints registry so sinks can render concise, domain-authored labels.
		CallHints map[tools.Ident]*template.Template

		// ResultHints optionally provides precompiled templates for result previews
		// keyed by tool ident. Templates receive the runtime-owned preview wrapper
		// where semantic data is available under `.Result` and bounded metadata
		// under `.Bounds`. When present, RegisterToolset installs these in the
		// global hints registry so sinks can render concise result previews.
		ResultHints map[tools.Ident]*template.Template

		// PayloadAdapter normalizes or enriches raw JSON payloads prior to decoding.
		// The adapter is applied exactly once at the activity boundary, or before
		// inline execution for Inline toolsets. When nil, no adaptation is applied.
		PayloadAdapter func(ctx context.Context, meta ToolCallMeta, tool tools.Ident, raw json.RawMessage) (json.RawMessage, error)

		// ResultMaterializer enriches typed tool results before the runtime encodes
		// them for hooks, workflow boundaries, or callers. When nil, the runtime
		// publishes the tool result exactly as produced by the executor.
		ResultMaterializer ResultMaterializer

		// DecodeInExecutor instructs the runtime to pass raw JSON payloads through to
		// the executor without pre-decoding. The executor must decode using generated
		// codecs. Defaults to false.
		DecodeInExecutor bool

		// TelemetryBuilder can be provided to build or enrich telemetry consistently
		// across transports. When set, the runtime may invoke it with timing/context.
		TelemetryBuilder func(ctx context.Context, meta ToolCallMeta, tool tools.Ident, start, end time.Time, extras map[string]any) *telemetry.ToolTelemetry

		// AgentTool, when non-nil, carries configuration for agent-as-tool toolsets.
		// It is populated by NewAgentToolsetRegistration so the workflow runtime can
		// start nested agent runs directly (fan-out/fan-in) without relying on the
		// synchronous Execute callback.
		AgentTool *AgentToolConfig
	}

	// RunPolicy configures per-agent runtime behavior (caps, time budgets, interrupts).
	// These values are evaluated during workflow execution to enforce limits and prevent
	// runaway tool loops or budget overruns.
	RunPolicy struct {
		// MaxToolCalls caps the total number of tool invocations per run (0 = unlimited).
		MaxToolCalls int

		// MaxConsecutiveFailedToolCalls caps sequential failures before aborting (0 = unlimited).
		MaxConsecutiveFailedToolCalls int

		// TimeBudget is the semantic wall-clock budget for planner and tool work
		// within the run (0 = unlimited). The runtime derives the engine run timeout
		// from this budget plus finalizer reserve and a small engine headroom.
		TimeBudget time.Duration

		// FinalizerGrace reserves time to produce a last assistant message after the
		// budget is exhausted. When set, the runtime stops scheduling new work once
		// the remaining time is less than or equal to this value and requests a final
		// response from the planner. Zero means no reserved window; defaults may apply.
		FinalizerGrace time.Duration

		// InterruptsAllowed indicates whether the workflow can be paused and resumed.
		InterruptsAllowed bool

		// OnMissingFields controls behavior when validation indicates missing fields:
		// "finalize" | "await_clarification" | "resume"
		OnMissingFields MissingFieldsAction

		// History, when non-nil, transforms the message history before each planner
		// invocation (PlanStart and PlanResume). It can truncate or compress history
		// while preserving system prompts and logical turn boundaries.
		History HistoryPolicy

		// Cache configures automatic prompt cache checkpoint placement.
		Cache CachePolicy
	}

	// CachePolicy configures automatic cache checkpoint placement for an agent.
	// The runtime applies this policy to model requests by populating
	// model.Request.Cache when it is nil so planners do not need to thread
	// CacheOptions through every call site. Providers that do not support
	// caching ignore these options.
	CachePolicy struct {
		// AfterSystem places a checkpoint after all system messages.
		AfterSystem bool

		// AfterTools places a checkpoint after tool definitions. Not all
		// providers support tool-level checkpoints (e.g., Nova does not).
		AfterTools bool
	}
)
