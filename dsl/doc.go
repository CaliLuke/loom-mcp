// Package dsl provides the loom-mcp design-time DSL for declaring agents, toolsets,
// MCP servers, registries, and run policies. These functions augment Goa's standard
// service DSL and drive the loom-mcp code generators; they are not used at runtime.
//
// # Overview
//
// The DSL enables design-first development of LLM-based agents. You declare your
// agent's capabilities, tools, and policies in Go code, then run `loom gen` to
// produce type-safe packages including:
//
//   - Agent packages with workflow definitions and planner activities
//   - Tool codecs, JSON schemas, and registry entries
//   - MCP server adapters and client helpers
//   - Agent-as-tool composition helpers
//
// Import the DSL alongside Goa's standard DSL:
//
//	import (
//	    . "github.com/CaliLuke/loom/dsl"
//	    . "github.com/CaliLuke/loom-mcp/dsl"
//	)
//
// # Mental Model
//
// Think of the DSL as declaring intent across three domains:
//
// **Agents** define LLM-powered planners that orchestrate tool usage. Each agent
// belongs to a Goa service and declares which toolsets it consumes (Use) and
// exports (Export) for other agents.
//
// **Toolsets** group related tools owned by services. Tools have typed schemas
// (Args/Return) and can be bound to service methods (BindTo) or implemented via
// custom executors. Toolsets can be sourced from local definitions, MCP servers,
// or remote registries.
//
// **Policies** constrain agent behavior at runtime: caps on tool calls, time
// budgets, history management, and prompt caching. These policies become
// configuration that the runtime enforces.
//
// # DSL Structure
//
// The DSL functions must be called in the appropriate context:
//
//	API("name", func() {})           // Top-level API definition (Goa)
//	DisableAgentDocs()               // Inside API - disable quickstart doc generation
//
//	var MyTools = Toolset("...", func() {...})  // Top-level toolset definition
//	var MCPTools = Toolset(FromMCP(...))        // MCP-backed toolset
//	var RegTools = Toolset(FromRegistry(...))   // Registry-backed toolset
//	var MyRegistry = Registry("...", func() {...})  // Registry definition
//
//	Service("name", func() {         // Goa service definition
//	    MCP("name", "version")       // Enable MCP for this service
//
//	    Agent("name", "desc", func() {   // Inside Service
//	        Use(MyTools)                 // Reference toolsets
//	        Export("tools", func() {...}) // Export toolsets
//	        RunPolicy(func() {           // Inside Agent
//	            DefaultCaps(...)         // Inside RunPolicy
//	            TimeBudget("5m")
//	            History(func() {...})
//	        })
//	    })
//
//	    Method("search", func() {    // Goa method definition
//	        Tool("search", "...")    // Mark as MCP tool (requires MCP enabled)
//	        Resource(...)            // Mark as MCP resource
//	    })
//	})
//
// # Key Functions by Category
//
// Agent Functions:
//   - [Agent] declares an LLM agent within a service
//   - [Use] declares toolset consumption
//   - [Export] declares toolset export for agent-as-tool
//   - [Workflow] defines a deterministic workflow planner
//   - [DisableAgentDocs] opts out of AGENTS_QUICKSTART.md generation
//   - [Passthrough] forwards exported tools to service methods
//   - [AgentToolset] references an exported toolset by coordinates
//   - [UseAgentToolset] references and consumes an exported agent toolset
//
// Workflow Functions:
//   - [Step] adds a deterministic tool-call node
//   - [FinalMessage] sets the completion message
//   - [Parallel] marks enclosed steps as concurrently ready
//   - [Join] declares a dependency barrier
//   - [RequestInput] declares a schema-typed human input node
//   - [Loop] declares a bounded repeated tool node
//   - [MaxIterations] caps loop iterations
//   - [UntilJSONPath] stops a loop when a JSONPath value matches
//   - [Branch] declares deterministic branch selection
//   - [Case] adds a branch case
//   - [BranchDefault] adds the fallback branch target
//
// Toolset Functions:
//   - [Toolset] defines a provider-owned tool collection
//   - [FromMCP] configures an MCP-backed toolset provider
//   - [FromRegistry] configures a registry-backed toolset provider
//   - [FromSkills] configures a local skill-backed model tool provider
//   - [SkillPreload] configures skill instruction preload behavior
//   - [SkillReload] configures skill file reload behavior
//   - [FromArtifacts] configures persisted run artifact tools
//   - [MaxArtifactBytes] caps loaded artifact content bytes
//   - [MaxArtifacts] caps listed artifacts
//   - [FromMemory] configures bounded memory lookup tools
//   - [MemoryMaxResults] caps memory lookup results
//   - [MemoryTranscript] exposes current-run transcript memory
//   - [MemoryIndexedTranscript] exposes indexed transcript memory
//   - [MemoryLongTerm] exposes long-term memory
//   - [MemoryVisibilityUser] scopes long-term memory to the resolved user
//   - [MemoryVisibilityShared] scopes long-term memory to shared knowledge
//   - [AgentToolset] references an exported toolset by coordinates
//   - [Tags] attaches metadata labels to tools or toolsets
//
// Tool Functions:
//   - [Tool] declares a callable tool
//   - [Args] defines input parameter schema
//   - [Return] defines output result schema
//   - [ServerData] defines typed server-only data emitted alongside results
//   - [FromMethodResultField] sources server data from a bound method result field
//   - [Audience] configures the server-data audience
//   - [AudienceTimeline] marks server data for timeline/UI rendering
//   - [AudienceInternal] marks server data for internal composition only
//   - [AudienceEvidence] marks server data as provenance evidence
//   - [BindTo] binds a tool to a service method
//   - [Inject] marks fields as server-injected (hidden from LLM)
//   - [CallHintTemplate] configures call display hint template
//   - [ResultHintTemplate] configures result display hint template
//   - [BoundedResult] marks result as a bounded view over larger data
//   - [Cursor] declares which payload field carries a paging cursor (optional)
//   - [NextCursor] declares which result field carries the next-page cursor (optional)
//   - [ResultReminder] configures a static post-result system reminder
//   - [TerminalRun] completes the run immediately after tool execution
//   - [Confirmation] declares that a tool must be confirmed out-of-band before execution
//   - [PromptTemplate] configures the confirmation prompt template
//   - [DeniedResultTemplate] configures the result returned when confirmation is denied
//   - [Expose] declares generated tool surfaces
//   - [MCPPlacement] places a projected toolset tool in an MCP server
//   - [Idempotent] marks an MCP method as safe to retry
//
// Policy Functions:
//   - [RunPolicy] configures execution constraints
//   - [DefaultCaps] sets resource limits using [MaxToolCalls] and [MaxConsecutiveFailedToolCalls]
//   - [TimeBudget] sets maximum execution duration
//   - [InterruptsAllowed] enables user interruption handling
//   - [OnMissingFields] configures validation behavior
//   - [Interceptors] attaches runtime interceptor identifiers
//   - [PreloadMemory] preloads transcript memory into planner context
//   - [PreloadLongTermMemory] preloads long-term memory into planner context
//   - [MemoryScopeCurrentRun] selects current-run transcript preload
//   - [MemoryScopeIndexed] selects indexed transcript preload
//   - [RetryAndReflect] configures planner retry/reflection behavior
//   - [MaxRetries] caps retry/reflection attempts
//   - [ErrorIfRetryExceeded] controls retry exhaustion behavior
//   - [History] configures conversation history management
//   - [Cache] configures prompt caching hints
//
// Timing Functions (inside RunPolicy):
//   - [Timing] groups timing configuration
//   - [Budget] sets total wall-clock budget
//   - [Plan] sets planner activity timeout
//   - [Tools] sets default tool activity timeout
//
// History Functions (inside History):
//   - [KeepRecentTurns] configures sliding window retention
//   - [CompressAtTurns] configures model-assisted summarization by turn count
//   - [CompressAtMaxInputTokens] configures model-assisted summarization by input tokens
//   - [KeepMaxTurns] caps retained turns after summarization
//   - [KeepMaxInputTokens] caps retained input tokens after summarization
//
// Cache Functions (inside Cache):
//   - [AfterSystem] places cache checkpoint after system messages
//   - [AfterTools] places cache checkpoint after tool definitions
//
// MCP Functions:
//   - [MCP] enables MCP protocol for a service
//   - [ProtocolVersion] configures MCP protocol version
//   - [WebsiteURL] configures the MCP implementation website URL
//   - [Icon] builds MCP icon metadata
//   - [IconMIMEType] sets icon MIME type metadata
//   - [IconSizes] sets supported icon sizes
//   - [IconTheme] sets icon theme metadata
//   - [ServerIcons] attaches implementation icons to an MCP server
//   - [ToolIcons] attaches icon metadata to an MCP tool
//   - [ToolTitle] sets the MCP tool display title
//   - [ToolDiscoveryCategory] sets the progressive discovery category
//   - [ToolDiscoveryTags] sets progressive discovery tags
//   - [ToolDiscoveryKeywords] sets progressive discovery keywords
//   - [ToolDiscoveryCallTemplateArg] adds optional arguments to generated call_tool examples
//   - [ToolSearch] configures progressive discovery search defaults
//   - [ToolSearchMaxResults] sets the search result cap
//   - [ToolSearchMinScore] sets the minimum search score
//   - [ToolSearchExactMatch] sets exact-match behavior
//   - [ToolSearchFuzzyNameMatching] toggles fuzzy name matching
//   - [ToolSearchBroadFallback] toggles broad fallback matching
//   - [ToolSearchWeights] configures search ranking weights
//   - [ToolSearchNameWeight] sets the name ranking weight
//   - [ToolSearchTitleWeight] sets the title ranking weight
//   - [ToolSearchMetadataWeight] sets the metadata ranking weight
//   - [ToolSearchDescriptionWeight] sets the description ranking weight
//   - [ToolSearchParameterWeight] sets the parameter ranking weight
//   - [ToolSearchFuzzyNameWeight] sets the fuzzy-name ranking weight
//   - [OAuth] declares OAuth protected-resource metadata
//   - [AuthorizationServer] adds an OAuth authorization server
//   - [OAuthScope] documents an OAuth scope
//   - [ResourceIdentifier] pins the protected-resource identifier
//   - [BearerMethodsSupported] lists supported bearer-token methods
//   - [ResourceDocumentationURL] sets the protected-resource docs URL
//   - [TrustProxyHeaders] opts into trusted proxy header handling
//   - [Resource] marks a method as an MCP resource
//   - [WatchableResource] marks a method as a subscribable MCP resource
//   - [ResourceIcons] attaches icon metadata to an MCP resource
//   - [SkillDirectory] exposes local agent skill roots as MCP resources
//   - [StaticPrompt] defines a static MCP prompt template
//   - [PromptIcons] attaches icon metadata to a static MCP prompt
//   - [RuntimePrompt] generates a runtime prompt spec from a static MCP prompt
//   - [RuntimePromptVersion] sets the runtime prompt spec version
//   - [DynamicPrompt] marks a method as a dynamic prompt generator
//   - [DynamicPromptIcons] attaches icon metadata to a dynamic MCP prompt
//   - [Notification] marks a method as an MCP notification sender
//   - [Subscription] defines a subscription handler
//   - [SubscriptionMonitor] defines an SSE subscription monitor
//
// Registry Functions:
//   - [Registry] declares a remote registry source
//   - [APIVersion] sets the registry API version
//   - [Retry] configures retry policy
//   - [SyncInterval] sets catalog refresh interval
//   - [CacheTTL] sets local cache duration
//   - [Federation] configures external registry imports
//   - [Include] specifies namespaces to import
//   - [Exclude] specifies namespaces to skip
//   - [PublishTo] rejects unsupported automatic registry publication
//
// # Generated Artifacts
//
// For each service with agents, `loom gen` produces:
//
//   - gen/<service>/agents/<agent>/ - Agent package with workflow and activities
//   - gen/<service>/agents/<agent>/specs/ - Tool specs, codecs, and JSON schemas
//   - gen/<service>/agents/<agent>/specs/tool_schemas.json - Backend-agnostic tool catalog
//   - gen/<service>/agents/<agent>/agenttools/ - Helpers for exported tools
//   - AGENTS_QUICKSTART.md - Contextual guide (unless disabled)
//
// For MCP-enabled services:
//
//   - gen/mcp_<service>/ - MCP server adapter and protocol helpers
//   - gen/mcp_<service>/client/ - Generated MCP client wrappers
//
// # Best Practices
//
// Design first: Put all agent and tool schemas in the DSL. Add examples and
// validations to field definitions. Let codegen own schemas and codecs.
//
// Use strong types: Define reusable Goa types (Type, ResultType) for complex
// tool payloads instead of inline anonymous schemas.
//
// Keep descriptions concise: Tool descriptions are shown to LLMs. Write clear,
// actionable summaries that help the model choose the right tool.
//
// Leverage BindTo: For service-backed tools, use BindTo to get generated
// transforms and keep tool schemas decoupled from method signatures.
//
// Mark bounded results: Tools returning potentially large data should use
// BoundedResult() so the runtime can track truncation metadata. Bounded tools
// keep their semantic result shape domain-specific and return canonical bounds
// through planner.ToolResult.Bounds.
//
// For complete documentation and examples, see docs/dsl.md in the repository.
package dsl
