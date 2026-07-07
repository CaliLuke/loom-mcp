package agent

import "fmt"

type (
	// MemoryToolSource selects a runtime memory source for FromMemory toolsets.
	MemoryToolSource string

	// MemoryVisibility selects user or shared long-term memory.
	MemoryVisibility string

	// SkillPreloadMode controls generated model-facing skill preload behavior.
	SkillPreloadMode string

	// SkillReloadMode controls generated model-facing skill reload behavior.
	SkillReloadMode string
)

const (
	providerLocalString   = "local"
	providerLocalEvalName = "local provider"
	providerArtifactsName = "artifacts"
	providerMemoryName    = "memory"
	providerSkillsName    = "skills"
)

const (
	// SkillPreloadNone does not preload skill instructions.
	SkillPreloadNone SkillPreloadMode = "none"
	// SkillPreloadOnStart preloads SKILL.md when generated registrations are built.
	SkillPreloadOnStart SkillPreloadMode = "on_start"

	// SkillReloadNever reuses cached loaded content.
	SkillReloadNever SkillReloadMode = "never"
	// SkillReloadPerCall reloads skill files for each model-facing load call.
	SkillReloadPerCall SkillReloadMode = "per_call"
)

// ProviderKind identifies the source/executor type for a toolset.
type ProviderKind int

const (
	// ProviderLocal indicates a toolset with inline schemas defined
	// directly in the DSL.
	ProviderLocal ProviderKind = iota
	// ProviderMCP indicates a toolset backed by an MCP server.
	ProviderMCP
	// ProviderRegistry indicates a toolset sourced from a registry.
	ProviderRegistry
	// ProviderSkills indicates a toolset sourced from local skill directories.
	ProviderSkills
	// ProviderArtifacts indicates a toolset backed by runtime artifact tools.
	ProviderArtifacts
	// ProviderMemory indicates a toolset backed by runtime memory tools.
	ProviderMemory
)

// String returns a human-readable representation of the provider kind.
func (k ProviderKind) String() string {
	switch k {
	case ProviderLocal:
		return providerLocalString
	case ProviderMCP:
		return "mcp"
	case ProviderRegistry:
		return "registry"
	case ProviderSkills:
		return providerSkillsName
	case ProviderArtifacts:
		return providerArtifactsName
	case ProviderMemory:
		return providerMemoryName
	default:
		return fmt.Sprintf("unknown(%d)", k)
	}
}

// ProviderExpr captures the provider configuration for a toolset,
// specifying where tool schemas come from and how tools are executed.
type ProviderExpr struct {
	// Kind identifies the provider type (local, MCP, registry).
	Kind ProviderKind
	// MCPService is the Goa service name that owns the MCP server
	// definition. Used when Kind is ProviderMCP.
	MCPService string
	// MCPToolset is the MCP server name for this toolset. Used when
	// Kind is ProviderMCP.
	MCPToolset string
	// Registry references the registry source for this toolset.
	// Used when Kind is ProviderRegistry.
	Registry *RegistryExpr
	// ToolsetName is the name of the toolset in the registry.
	// Used when Kind is ProviderRegistry.
	ToolsetName string
	// Version pins the toolset to a specific version.
	// Used when Kind is ProviderRegistry.
	Version string
	// SkillRoots are filesystem directories containing child skill directories.
	// Used when Kind is ProviderSkills.
	SkillRoots []string
	// SkillPreload controls generated model-facing skill preload behavior.
	SkillPreload SkillPreloadMode
	// SkillReload controls generated model-facing skill reload behavior.
	SkillReload SkillReloadMode
	// ArtifactMaxBytes caps artifact load responses.
	// Used when Kind is ProviderArtifacts.
	ArtifactMaxBytes int
	// ArtifactMaxCount caps artifact list responses.
	// Used when Kind is ProviderArtifacts.
	ArtifactMaxCount int
	// MemoryMaxResults caps memory tool responses.
	// Used when Kind is ProviderMemory.
	MemoryMaxResults int
	// MemorySources selects explicit memory sources. Empty preserves transcript
	// plus indexed transcript compatibility.
	MemorySources []MemoryToolSource
	// MemoryVisibility selects the widest long-term memory visibility.
	MemoryVisibility MemoryVisibility
}

const (
	// MemoryToolSourceTranscript exposes current-run transcript memory.
	MemoryToolSourceTranscript MemoryToolSource = "transcript"
	// MemoryToolSourceIndexedTranscript exposes indexed transcript memory.
	MemoryToolSourceIndexedTranscript MemoryToolSource = "indexed_transcript"
	// MemoryToolSourceLongTerm exposes long-term entry memory.
	MemoryToolSourceLongTerm MemoryToolSource = "long_term"

	// MemoryVisibilityUser scopes long-term memory to one user.
	MemoryVisibilityUser MemoryVisibility = "user"
	// MemoryVisibilityShared scopes long-term memory to shared knowledge.
	MemoryVisibilityShared MemoryVisibility = "shared"
)

// EvalName returns a descriptive identifier for error reporting.
func (p *ProviderExpr) EvalName() string {
	switch p.Kind {
	case ProviderLocal:
		return providerLocalEvalName
	case ProviderMCP:
		return fmt.Sprintf("MCP provider (service=%q, toolset=%q)", p.MCPService, p.MCPToolset)
	case ProviderRegistry:
		regName := ""
		if p.Registry != nil {
			regName = p.Registry.Name
		}
		return fmt.Sprintf("registry provider (registry=%q, toolset=%q)", regName, p.ToolsetName)
	case ProviderSkills:
		return fmt.Sprintf("%s provider (roots=%d)", providerSkillsName, len(p.SkillRoots))
	case ProviderArtifacts:
		return fmt.Sprintf("artifacts provider (max_bytes=%d, max_artifacts=%d)", p.ArtifactMaxBytes, p.ArtifactMaxCount)
	case ProviderMemory:
		return fmt.Sprintf("memory provider (max_results=%d)", p.MemoryMaxResults)
	default:
		return providerLocalEvalName
	}
}
