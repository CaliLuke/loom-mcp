package dsl

import (
	"strings"

	"github.com/CaliLuke/loom/eval"

	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
)

// Toolset defines a provider-owned group of related tools. Declare toolsets at
// the top level using Toolset(...) and reference them from agents via
// Use / Export.
//
// Tools declared inside a Toolset may be:
//
//   - Bound to Goa service methods via BindTo, in which case codegen emits
//     transforms and client helpers.
//   - Backed by MCP tools declared with the MCP DSL (MCP + Tool) and
//     exposed via Toolset with FromMCP provider option.
//   - Implemented by custom executors or agent logic when left unbound.
//
// Toolset accepts a single form:
//
//   - Toolset("name", func()) declares a new toolset with the given name and tools.
//
// Example (provider toolset definition):
//
//	var CommonTools = Toolset("common", func() {
//	    Tool("notify", "Send notification", func() {
//	        Args(func() {
//	            Attribute("message", String, "Message to send")
//	            Required("message")
//	        })
//	    })
//	})
//
// Agents consume this toolset via Use:
//
//	Agent("assistant", "helper", func() {
//	    Use(CommonTools, func() {
//	        Tool("notify") // reference existing tool by name
//	    })
//	})
//
// For MCP-backed toolsets, use FromMCP provider option:
//
//	var MCPTools = Toolset(FromMCP("assistant-service", "assistant-mcp"))
//
// For registry-backed toolsets, use FromRegistry provider option:
//
//	var RegistryTools = Toolset(FromRegistry(CorpRegistry, "data-tools"))
//
// Toolset accepts these forms:
//   - Toolset("name", func()) - local toolset with inline schemas
//   - Toolset(FromMCP(service, toolset)) - MCP-backed toolset (name derived from toolset)
//   - Toolset(FromRegistry(registry, toolset)) - registry-backed toolset (name derived from toolset)
//   - Toolset("name", FromMCP(...)) - MCP-backed with explicit name
//   - Toolset("name", FromRegistry(...)) - registry-backed with explicit name
//   - Toolset(FromMCP(...), func()) - MCP-backed with additional config
func Toolset(args ...any) *agentsexpr.ToolsetExpr {
	if _, ok := eval.Current().(eval.TopExpr); !ok {
		incompatibleDSL("Toolset")
		return nil
	}

	var name string
	var provider *agentsexpr.ProviderExpr
	var dsl func()

	for _, arg := range args {
		switch a := arg.(type) {
		case string:
			name = a
		case *agentsexpr.ProviderExpr:
			provider = a
		case func():
			dsl = a
		default:
			eval.InvalidArgError("name, provider option, or func()", arg)
			return nil
		}
	}

	if name == "" && provider != nil {
		name = toolsetNameFromProvider(provider)
	}

	if name == "" {
		eval.ReportError("toolset name must be non-empty")
		return nil
	}

	ts := newToolsetDefinition(name, dsl)
	ts.Provider = provider

	agentsexpr.Root.Toolsets = append(agentsexpr.Root.Toolsets, ts)
	return ts
}

// FromMCP configures a toolset to be backed by an MCP server. Use FromMCP
// as a provider option when declaring a Toolset.
//
// FromMCP takes:
//   - service: Goa service name that owns the MCP server
//   - toolset: MCP server name (also used as the toolset name if not specified)
//
// Example:
//
//	var MCPTools = Toolset(FromMCP("assistant-service", "assistant-mcp"))
//
// Or with an explicit name:
//
//	var MCPTools = Toolset("my-tools", FromMCP("assistant-service", "assistant-mcp"))
func FromMCP(service, toolset string) *agentsexpr.ProviderExpr {
	if service == "" {
		eval.ReportError("FromMCP requires non-empty service name")
		return nil
	}
	if toolset == "" {
		eval.ReportError("FromMCP requires non-empty toolset name")
		return nil
	}
	return &agentsexpr.ProviderExpr{
		Kind:       agentsexpr.ProviderMCP,
		MCPService: service,
		MCPToolset: toolset,
	}
}

// SkillProviderOption configures FromSkills.
type SkillProviderOption func(*agentsexpr.ProviderExpr)

const (
	// SkillPreloadNone leaves skill instructions unloaded until requested.
	SkillPreloadNone = agentsexpr.SkillPreloadNone
	// SkillPreloadOnStart preloads SKILL.md when the generated registration is built.
	SkillPreloadOnStart = agentsexpr.SkillPreloadOnStart
	// SkillReloadNever reuses loaded content until the registration is rebuilt.
	SkillReloadNever = agentsexpr.SkillReloadNever
	// SkillReloadPerCall reloads skill files for each load call.
	SkillReloadPerCall = agentsexpr.SkillReloadPerCall
)

// FromSkills configures a toolset to expose local agent skills as model-facing
// tools. Each root must contain child skill directories with SKILL.md files.
//
// Example:
//
//	var Skills = Toolset(FromSkills(".agents/skills"))
//
// Or with an explicit name:
//
//	var AssistantSkills = Toolset("assistant.skills", FromSkills(".agents/skills", SkillPreload(SkillPreloadOnStart)))
func FromSkills(args ...any) *agentsexpr.ProviderExpr {
	provider := &agentsexpr.ProviderExpr{
		Kind:        agentsexpr.ProviderSkills,
		SkillReload: agentsexpr.SkillReloadNever,
	}
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			root := strings.TrimSpace(value)
			if root != "" {
				provider.SkillRoots = append(provider.SkillRoots, root)
			}
		case SkillProviderOption:
			if value != nil {
				value(provider)
			}
		default:
			eval.ReportError("FromSkills accepts skill roots and SkillProviderOption values, got %T", arg)
		}
	}
	if len(provider.SkillRoots) == 0 {
		eval.ReportError("FromSkills requires at least one non-empty skill root")
		return nil
	}
	return provider
}

// SkillPreload configures when model-facing skill instructions are preloaded.
func SkillPreload(mode agentsexpr.SkillPreloadMode) SkillProviderOption {
	return func(provider *agentsexpr.ProviderExpr) {
		provider.SkillPreload = mode
	}
}

// SkillReload configures when model-facing skill files are reloaded from disk.
func SkillReload(mode agentsexpr.SkillReloadMode) SkillProviderOption {
	return func(provider *agentsexpr.ProviderExpr) {
		provider.SkillReload = mode
	}
}

// ArtifactProviderOption configures FromArtifacts.
type ArtifactProviderOption func(*agentsexpr.ProviderExpr)

// FromArtifacts configures a toolset to expose persisted run artifacts as
// model-facing tools.
func FromArtifacts(opts ...ArtifactProviderOption) *agentsexpr.ProviderExpr {
	provider := &agentsexpr.ProviderExpr{Kind: agentsexpr.ProviderArtifacts}
	for _, opt := range opts {
		if opt != nil {
			opt(provider)
		}
	}
	return provider
}

// MaxArtifactBytes caps load_artifact response content bytes.
func MaxArtifactBytes(n int) ArtifactProviderOption {
	return func(provider *agentsexpr.ProviderExpr) {
		provider.ArtifactMaxBytes = n
	}
}

// MaxArtifacts caps list_artifacts response count.
func MaxArtifacts(n int) ArtifactProviderOption {
	return func(provider *agentsexpr.ProviderExpr) {
		provider.ArtifactMaxCount = n
	}
}

// MemoryProviderOption configures FromMemory.
type MemoryProviderOption interface {
	applyMemoryProvider(*agentsexpr.ProviderExpr)
}

type memoryMaxResultsOption int
type memorySourceOption agentsexpr.MemoryToolSource
type memoryVisibilityOption agentsexpr.MemoryVisibility

// FromMemory configures a toolset to expose bounded memory lookup tools.
func FromMemory(opts ...MemoryProviderOption) *agentsexpr.ProviderExpr {
	provider := &agentsexpr.ProviderExpr{Kind: agentsexpr.ProviderMemory}
	for _, opt := range opts {
		if opt != nil {
			opt.applyMemoryProvider(provider)
		}
	}
	return provider
}

// MemoryMaxResults caps memory results for memory tools or preload.
func MemoryMaxResults(n int) memoryMaxResultsOption {
	return memoryMaxResultsOption(n)
}

func (o memoryMaxResultsOption) applyMemoryProvider(provider *agentsexpr.ProviderExpr) {
	provider.MemoryMaxResults = int(o)
}

// MemoryTranscript exposes current-run transcript memory from FromMemory.
func MemoryTranscript() MemoryProviderOption {
	return memorySourceOption(agentsexpr.MemoryToolSourceTranscript)
}

// MemoryIndexedTranscript exposes indexed transcript memory from FromMemory.
func MemoryIndexedTranscript() MemoryProviderOption {
	return memorySourceOption(agentsexpr.MemoryToolSourceIndexedTranscript)
}

// MemoryLongTerm exposes long-term entry memory from FromMemory.
func MemoryLongTerm() MemoryProviderOption {
	return memorySourceOption(agentsexpr.MemoryToolSourceLongTerm)
}

// MemoryVisibilityUser scopes long-term memory to the resolved user.
func MemoryVisibilityUser() memoryVisibilityOption {
	return memoryVisibilityOption(agentsexpr.MemoryVisibilityUser)
}

// MemoryVisibilityShared scopes long-term memory to explicitly shared knowledge.
func MemoryVisibilityShared() memoryVisibilityOption {
	return memoryVisibilityOption(agentsexpr.MemoryVisibilityShared)
}

func (o memorySourceOption) applyMemoryProvider(provider *agentsexpr.ProviderExpr) {
	provider.MemorySources = append(provider.MemorySources, agentsexpr.MemoryToolSource(o))
}

func (o memoryVisibilityOption) applyMemoryProvider(provider *agentsexpr.ProviderExpr) {
	provider.MemoryVisibility = agentsexpr.MemoryVisibility(o)
}

// FromRegistry configures a toolset to be sourced from a registry. Use
// FromRegistry as a provider option when declaring a Toolset.
//
// FromRegistry takes:
//   - registry: the RegistryExpr returned by Registry()
//   - toolset: name of the toolset in the registry (also used as the toolset name if not specified)
//
// Example:
//
//	var CorpRegistry = Registry("corp", func() {
//	    URL("https://registry.corp.internal")
//	})
//
//	var RegistryTools = Toolset(FromRegistry(CorpRegistry, "data-tools"))
//
// Or with an explicit name:
//
//	var RegistryTools = Toolset("my-tools", FromRegistry(CorpRegistry, "data-tools"))
//
// For version pinning, use the Version DSL inside the Toolset:
//
//	var PinnedTools = Toolset(FromRegistry(CorpRegistry, "data-tools"), func() {
//	    Version("1.2.3")
//	})
func FromRegistry(registry *agentsexpr.RegistryExpr, toolset string) *agentsexpr.ProviderExpr {
	if registry == nil {
		eval.ReportError("FromRegistry requires a non-nil registry")
		return nil
	}
	if toolset == "" {
		eval.ReportError("FromRegistry requires non-empty toolset name")
		return nil
	}
	return &agentsexpr.ProviderExpr{
		Kind:        agentsexpr.ProviderRegistry,
		Registry:    registry,
		ToolsetName: toolset,
	}
}

// buildToolsetExpr constructs a ToolsetExpr from a value and DSL function.
func newToolsetDefinition(name string, dsl func()) *agentsexpr.ToolsetExpr {
	return &agentsexpr.ToolsetExpr{
		Name:    name,
		DSLFunc: dsl,
	}
}

func toolsetNameFromProvider(provider *agentsexpr.ProviderExpr) string {
	switch provider.Kind {
	case agentsexpr.ProviderLocal:
		return ""
	case agentsexpr.ProviderMCP:
		return provider.MCPToolset
	case agentsexpr.ProviderRegistry:
		return provider.ToolsetName
	case agentsexpr.ProviderSkills:
		return "skills"
	case agentsexpr.ProviderArtifacts:
		return "artifacts"
	case agentsexpr.ProviderMemory:
		return "memory"
	default:
		return ""
	}
}

func cloneToolset(origin *agentsexpr.ToolsetExpr, agent *agentsexpr.AgentExpr, overlay func()) *agentsexpr.ToolsetExpr {
	if origin == nil {
		eval.ReportError("toolset reference cannot be nil")
		return nil
	}
	dup := &agentsexpr.ToolsetExpr{
		Name:        origin.Name,
		Description: origin.Description,
		Tags:        append([]string(nil), origin.Tags...),
		Agent:       agent,
		Provider:    cloneProvider(origin.Provider),
	}
	if origin.AgentToolset != nil && origin.Origin == nil {
		ref := *origin.AgentToolset
		dup.AgentToolset = &ref
	} else {
		dup.Origin = origin
	}
	dup.DSLFunc = overlay
	return dup
}

func cloneProvider(origin *agentsexpr.ProviderExpr) *agentsexpr.ProviderExpr {
	if origin == nil {
		return nil
	}
	dup := *origin
	dup.SkillRoots = append([]string(nil), origin.SkillRoots...)
	dup.MemorySources = append([]agentsexpr.MemoryToolSource(nil), origin.MemorySources...)
	return &dup
}

func instantiateToolset(value any, overlay func(), agent *agentsexpr.AgentExpr) *agentsexpr.ToolsetExpr {
	switch v := value.(type) {
	case string:
		if v == "" {
			eval.ReportError("toolset name must be non-empty")
			return nil
		}
		return &agentsexpr.ToolsetExpr{
			Name:    v,
			DSLFunc: overlay,
			Agent:   agent,
		}
	case *agentsexpr.ToolsetExpr:
		return cloneToolset(v, agent, overlay)
	default:
		eval.ReportError("toolset must be referenced by name or Toolset expression")
		return nil
	}
}

// AgentToolset references a toolset exported by another agent identified by
// service and agent names. Use inside an Agent's Uses block to explicitly
// depend on an exported toolset when inference is not possible or ambiguous.
//
// When to use AgentToolset vs Toolset:
//   - Prefer Toolset(X) when you already have an expression handle (e.g., a
//     top-level Toolset variable or an agent's exported Toolset). loom-mcp will
//     infer a RemoteAgent provider automatically when exactly one agent in a
//     different service Exports a toolset with the same name.
//   - Use AgentToolset(service, agent, toolset) when you:
//   - Do not have an expression handle to the exported toolset, or
//   - Have ambiguity (multiple agents export a toolset with the same name), or
//   - Want to be explicit in the design for clarity.
//
// AgentToolset(service, agent, toolset)
//   - service: Goa service name that owns the exporting agent
//   - agent:   Agent name in that service
//   - toolset: Exported toolset name in that agent
//
// The referenced toolset is resolved from the design, and a local reference is
// recorded with its Origin set to the defining toolset. Provider information is
// inferred during validation and will classify this as a RemoteAgent provider
// when the owner service differs from the consumer service.
func AgentToolset(service, agent, toolset string) *agentsexpr.ToolsetExpr {
	if service == "" || agent == "" || toolset == "" {
		eval.ReportError("AgentToolset requires non-empty service, agent, and toolset")
		return nil
	}
	return &agentsexpr.ToolsetExpr{
		Name: toolset,
		AgentToolset: &agentsexpr.AgentToolsetReferenceExpr{
			Service: service,
			Agent:   agent,
			Toolset: toolset,
		},
	}
}

// UseAgentToolset is an alias for AgentToolset. Prefer AgentToolset in new
// designs; this alias exists for readability in some codebases.
//
// Deprecated: Use AgentToolset instead. This function will be removed in a
// future release.
func UseAgentToolset(service, agent, toolset string) *agentsexpr.ToolsetExpr {
	ts := AgentToolset(service, agent, toolset)
	if ts == nil {
		return nil
	}
	return Use(ts)
}
