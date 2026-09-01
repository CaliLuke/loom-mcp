package codegen

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/codegen/shared"
	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
)

// Generate is the code generation entry point for the agents plugin. It is called
// by the Goa code generation framework during the `loom gen` command execution.
//
// The function scans the provided DSL roots for agent declarations, transforms
// them into template-ready data, and emits the current owner-scoped layout:
//   - gen/<service>/toolsets/<toolset>/: shared specs, codecs, transforms, and providers
//   - gen/<service>/agents/<agent>/: agent, config, registry, and aggregate specs
//   - gen/<service>/agents/<agent>/projected/: projected service execution helpers
//   - gen/<service>/agents/<agent>/agenttools/: exported agent-as-tool helpers
//   - gen/<service>/registries/<registry>/: declared registry clients
//   - AGENTS_QUICKSTART.md: module-root generated wiring guide when enabled
//
// Parameters:
//   - genpkg: Go import path to the generated code root (e.g., "myapp/gen")
//   - roots: evaluated DSL roots; must include both goaexpr.RootExpr (for services)
//     and agentsExpr.Root (for agents)
//   - files: existing generated files from other Goa plugins; agent files are appended
//
// Returns the input files slice with agent-generated files appended. If no agents are
// declared in the DSL, returns the input slice unchanged. Returns an error if:
//   - The agents root cannot be located in roots
//   - A service referenced by an agent is not found
//   - Template rendering fails
//   - Tool spec generation fails
//
// The function is safe to call multiple times during generation but expects DSL
// evaluation to be complete before invocation.
func Generate(genpkg string, roots []eval.Root, files []*codegen.File) ([]*codegen.File, error) {
	data, err := buildGeneratorData(genpkg, roots)
	if err != nil {
		return nil, err
	}
	if len(data.Services) == 0 {
		return files, nil
	}

	var generated []*codegen.File
	specsCache := newToolSpecsDataCache()

	// Emit owner-scoped toolset specs/codecs once per defining toolset.
	specFiles, err := toolsetSpecsFiles(data, specsCache)
	if err != nil {
		return nil, err
	}
	generated = append(generated, specFiles...)

	for _, svc := range data.Services {
		// Emit registry client packages for declared registries.
		if regFiles := registryClientFiles(genpkg, svc); len(regFiles) > 0 {
			generated = append(generated, regFiles...)
		}

		for _, agent := range svc.Agents {
			afiles, err := agentFiles(agent, specsCache)
			if err != nil {
				return nil, err
			}
			generated = append(generated, afiles...)
		}
	}

	// Emit contextual quickstart README at module root unless disabled via DSL.
	if !agentsExpr.Root.DisableAgentDocs {
		if qf := quickstartReadmeFile(data); qf != nil {
			generated = append(generated, qf)
		}
	}

	return append(files, generated...), nil
}

// agentSpecsAggregatorFile emits specs/specs.go that aggregates Specs and metadata
// from all specs/<toolset> packages into a single package for convenience.
func agentSpecsAggregatorFile(agent *AgentData) *codegen.File {
	// Build import list: runtime + per-toolset packages.
	imports := []*codegen.ImportSpec{
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools", Name: "tools"},
	}
	// Claim every fixed import identifier in an alias scope so toolset specs
	// packages named after runtime packages (for example "tools" or "policy")
	// and toolsets whose slugs sanitize identically under different owners all
	// receive unique aliases.
	aliasScope := codegen.NewNameScope()
	for _, imp := range imports {
		name := imp.Name
		if name == "" {
			name = path.Base(imp.Path)
		}
		aliasScope.Unique(name)
	}
	added := make(map[string]struct{})
	toolsets := make([]*ToolsetData, 0, len(agent.AllToolsets))
	for _, ts := range agent.AllToolsets {
		if len(ts.Tools) == 0 || ts.SpecsImportPath == "" {
			continue
		}
		if _, ok := added[ts.SpecsImportPath]; ok {
			continue
		}
		alias := aliasScope.Unique(ts.SpecsPackageName, "specs")
		imports = append(imports, &codegen.ImportSpec{Path: ts.SpecsImportPath, Name: alias})
		added[ts.SpecsImportPath] = struct{}{}
		// Update toolset data with the alias for template use
		tsCopy := *ts
		tsCopy.SpecsPackageName = alias
		toolsets = append(toolsets, &tsCopy)
	}
	if len(toolsets) == 0 {
		return nil
	}
	sections := []codegen.Section{
		codegen.Header(agent.StructName+" aggregated tool specs", "specs", imports),
		toolSpecsAggregateSection(toolSpecsAggregateData{Toolsets: toolsets}),
	}
	return &codegen.File{Path: filepath.Join(agent.Dir, "specs", "specs.go"), Sections: sections}
}

func resolvedAgentSpecsAggregatorFile(agent *AgentData, specsCache *toolSpecsDataCache) (*codegen.File, error) {
	resolved := *agent
	resolved.AllToolsets = make([]*ToolsetData, 0, len(agent.AllToolsets))
	for _, ts := range agent.AllToolsets {
		if ts == nil || len(ts.Tools) == 0 || ts.SpecsImportPath == "" {
			continue
		}
		specs, err := specsCache.specsForToolset(agent.Genpkg, ts)
		if err != nil {
			return nil, fmt.Errorf("agent codegen: build aggregated specs for agent %q toolset %q: %w", agent.Name, ts.QualifiedName, err)
		}
		entries := make(map[string]*toolEntry, len(specs.tools))
		for _, entry := range specs.tools {
			entries[entry.Name] = entry
		}
		resolvedToolset := *ts
		resolvedToolset.Tools = make([]*ToolData, 0, len(ts.Tools))
		for _, tool := range ts.Tools {
			entry, ok := entries[tool.QualifiedName]
			if !ok {
				return nil, fmt.Errorf("agent codegen: missing aggregated spec entry for tool %q", tool.QualifiedName)
			}
			resolvedTool := *tool
			resolvedTool.ConstName = entry.ConstName
			resolvedToolset.Tools = append(resolvedToolset.Tools, &resolvedTool)
		}
		resolved.AllToolsets = append(resolved.AllToolsets, &resolvedToolset)
	}
	return agentSpecsAggregatorFile(&resolved), nil
}

func agentImplFile(agent *AgentData) *codegen.File {
	imports := []*codegen.ImportSpec{
		{Path: "errors"},
		{Path: "strings"},
		{Path: "context"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent", Name: "agent"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "runtime"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
	}
	if agent.Workflow != nil {
		imports = append(imports,
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson", Name: "rawjson"},
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools", Name: "tools"},
		)
	}
	sections := []codegen.Section{
		codegen.Header(agent.StructName+" implementation", agent.PackageName, imports),
		agentImplSection(agent),
	}
	return &codegen.File{Path: filepath.Join(agent.Dir, "agent.go"), Sections: sections}
}

func agentConfigFile(agent *AgentData) *codegen.File {
	imports := []*codegen.ImportSpec{
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
	}
	if agent.Workflow == nil || (agent.RunPolicy.History != nil && agent.RunPolicy.History.Mode == "compress") {
		imports = append(imports, &codegen.ImportSpec{Path: "errors"})
	}
	// Import model client when a compress-history policy is configured so the
	// generated config can reference model.Client in the HistoryModel field.
	if agent.RunPolicy.History != nil && agent.RunPolicy.History.Mode == "compress" {
		imports = append(imports,
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model", Name: "model"},
		)
	}
	// Determine whether fmt is needed. The config Validate() uses fmt.Errorf for
	// missing method-backed toolset dependencies and for MCP callers.
	needsFmt := false
	if len(agent.MCPToolsets) > 0 {
		needsFmt = true
		imports = append(imports,
			&codegen.ImportSpec{Name: "mcpruntime", Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"},
		)
	}
	// Scan toolsets to see if any tool is method-backed; if so, fmt is also required.
	if !needsFmt {
		for _, ts := range agent.AllToolsets {
			for _, t := range ts.Tools {
				if t.IsMethodBacked {
					needsFmt = true
					break
				}
			}
			if needsFmt {
				break
			}
		}
	}
	if needsFmt {
		imports = append(imports, &codegen.ImportSpec{Path: "fmt"})
	}
	// Import toolset packages that define method-backed tools so config can reference their Config types.
	for _, ts := range agent.AllToolsets {
		has := false
		for _, t := range ts.Tools {
			if t.IsMethodBacked {
				has = true
				break
			}
		}
		if has && ts.PackageImportPath != "" {
			imports = append(imports, &codegen.ImportSpec{Path: ts.PackageImportPath, Name: ts.PackageName})
		}
	}
	sections := []codegen.Section{
		codegen.Header(agent.StructName+" config", agent.PackageName, imports),
		agentConfigSection(agent),
	}
	return &codegen.File{Path: filepath.Join(agent.Dir, "config.go"), Sections: sections}
}

func agentRegistryFile(agent *AgentData) *codegen.File {
	hasExternal := false
	for _, ts := range agent.AllToolsets {
		if isMCPBackedToolset(ts) {
			hasExternal = true
			break
		}
	}
	hasExecutorBacked := false
	for _, ts := range agent.UsedToolsets {
		if needsExecutorBackedRegistration(ts) {
			hasExecutorBacked = true
			break
		}
	}
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "errors"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "agentsruntime"},
	}
	if hasExternal || hasExecutorBacked {
		imports = append(imports,
			&codegen.ImportSpec{Path: "fmt"},
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
		)
	}
	if needsMemoryImport(agent) {
		imports = append(imports, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"})
	}
	// Import toolset packages that have method-backed tools so we can call their registration helpers.
	for _, ts := range agent.AllToolsets {
		// Import for method-backed (app-supplied executor) or external MCP (local executor)
		needs := false
		for _, t := range ts.Tools {
			if t.IsMethodBacked {
				needs = true
				break
			}
		}
		if isMCPBackedToolset(ts) {
			needs = true
		}
		if needs && ts.PackageImportPath != "" {
			imports = append(imports, &codegen.ImportSpec{Path: ts.PackageImportPath, Name: ts.PackageName})
		}
	}
	// Import tools/hints only when a non-MCP Used toolset without agenttools
	// helpers actually defines hint templates. The registry template now omits
	// hint code entirely when no templates are present.
	needsTools := false
	for _, ts := range agent.UsedToolsets {
		if isMCPBackedToolset(ts) || isSkillsBackedToolset(ts) || isArtifactsBackedToolset(ts) || isMemoryBackedToolset(ts) {
			continue
		}
		if ts.AgentToolsImportPath != "" {
			continue
		}
		if !toolsetHasHintTemplates(ts) {
			continue
		}
		needsTools = true
		break
	}
	if needsTools {
		imports = append(imports,
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"},
			&codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints", Name: "hints"},
		)
	}
	if needsTimeImport(agent) {
		imports = append(imports, &codegen.ImportSpec{Path: "time"})
	}
	if len(agent.Tools) > 0 {
		imports = append(imports, &codegen.ImportSpec{Path: agent.ToolSpecsImportPath, Name: agent.ToolSpecsPackage})
	}
	usedAliases := make(map[string]struct{})
	for _, imp := range imports {
		alias := imp.Name
		if alias == "" {
			alias = path.Base(imp.Path)
		}
		if alias == "" {
			continue
		}
		usedAliases[alias] = struct{}{}
	}
	nextAlias := func(base string) string {
		alias := base
		if alias == "" {
			alias = "specs"
		}
		if _, exists := usedAliases[alias]; !exists {
			usedAliases[alias] = struct{}{}
			return alias
		}
		suffix := 2
		for {
			candidate := fmt.Sprintf("%s%d", alias, suffix)
			if _, exists := usedAliases[candidate]; !exists {
				usedAliases[candidate] = struct{}{}
				return candidate
			}
			suffix++
		}
	}
	// RegisterAgent/RegisterUsedToolsets bind each used toolset registration to
	// that toolset's own specs package so runtime codecs and hint templates use
	// the canonical payload/result contracts.
	usedSpecsImports := make(map[string]struct{})
	usedSpecsAliases := make(map[string]string)
	for _, ts := range agent.UsedToolsets {
		if ts.AgentToolsImportPath != "" || isSkillsBackedToolset(ts) || isArtifactsBackedToolset(ts) || isMemoryBackedToolset(ts) || ts.SpecsImportPath == "" || ts.SpecsPackageName == "" {
			continue
		}
		alias, ok := usedSpecsAliases[ts.QualifiedName]
		if !ok {
			alias = nextAlias(ts.SpecsPackageName)
			usedSpecsAliases[ts.QualifiedName] = alias
		}
		if _, seen := usedSpecsImports[ts.SpecsImportPath]; seen {
			continue
		}
		imports = append(imports, &codegen.ImportSpec{
			Path: ts.SpecsImportPath,
			Name: alias,
		})
		usedSpecsImports[ts.SpecsImportPath] = struct{}{}
	}
	agentForRegistry := *agent
	if len(usedSpecsAliases) > 0 {
		agentForRegistry.UsedToolsets = cloneToolsetsWithSpecsAliases(agent.UsedToolsets, usedSpecsAliases)
		agentForRegistry.AllToolsets = cloneToolsetsWithSpecsAliases(agent.AllToolsets, usedSpecsAliases)
	}
	sections := []codegen.Section{
		codegen.Header(agent.StructName+" registry", agent.PackageName, imports),
		agentRegistrySection(struct {
			*AgentData
			HasExternalMCP bool
		}{AgentData: &agentForRegistry, HasExternalMCP: hasExternal}),
	}
	return &codegen.File{
		Path:     filepath.Join(agent.Dir, "registry.go"),
		Sections: sections,
	}
}

func cloneToolsetsWithSpecsAliases(toolsets []*ToolsetData, aliases map[string]string) []*ToolsetData {
	if len(toolsets) == 0 {
		return toolsets
	}
	copies := make([]*ToolsetData, 0, len(toolsets))
	for _, ts := range toolsets {
		if ts == nil {
			continue
		}
		tsCopy := *ts
		if alias, ok := aliases[ts.QualifiedName]; ok {
			tsCopy.SpecsPackageName = alias
		}
		copies = append(copies, &tsCopy)
	}
	return copies
}

func activityNeedsTime(act ActivityArtifact) bool {
	return act.ScheduleToStartTimeout > 0 ||
		act.StartToCloseTimeout > 0 ||
		act.HeartbeatTimeout > 0 ||
		act.RetryPolicy.InitialInterval > 0
}

func agentActivitiesNeedTimeImport(agent *AgentData) bool {
	for _, act := range agent.Runtime.Activities {
		if activityNeedsTime(act) {
			return true
		}
	}
	return false
}

func needsTimeImport(agent *AgentData) bool {
	if agent.RunPolicy.TimeBudget > 0 {
		return true
	}
	return agentActivitiesNeedTimeImport(agent)
}

func needsMemoryImport(agent *AgentData) bool {
	if agent.RunPolicy.PreloadLongTermMemory != nil {
		return true
	}
	for _, ts := range agent.UsedToolsets {
		if !isMemoryBackedToolset(ts) || ts.Expr == nil || ts.Expr.Provider == nil {
			continue
		}
		if len(ts.Expr.Provider.MemorySources) > 0 || ts.Expr.Provider.MemoryVisibility != "" {
			return true
		}
	}
	return false
}

func agentToolsFiles(agent *AgentData, specsCache *toolSpecsDataCache) ([]*codegen.File, error) {
	if len(agent.ExportedToolsets) == 0 {
		return nil, nil
	}
	files := make([]*codegen.File, 0, len(agent.ExportedToolsets))
	for _, ts := range agent.ExportedToolsets {
		if ts.AgentToolsDir == "" {
			continue
		}
		// Build tool entries so templates can reuse the same type/codec naming
		// decisions as specs generation.
		specs, err := specsCache.specsForToolset(agent.Genpkg, ts)
		if err != nil {
			return nil, fmt.Errorf("agent codegen: build exported toolset specs for agent %q toolset %q: %w", agent.Name, ts.QualifiedName, err)
		}
		if specs == nil {
			continue
		}
		data := agentToolsetFileData{
			PackageName: ts.AgentToolsPackage,
			Toolset:     ts,
			Tools:       specs.tools,
		}
		imports := []*codegen.ImportSpec{
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "runtime"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent", Name: "agent"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
			// Per-toolset specs package for typed payloads
			{Path: ts.SpecsImportPath, Name: ts.SpecsPackageName + "specs"},
		}
		if toolsetHasHintTemplates(ts) {
			imports = append(imports, &codegen.ImportSpec{
				Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints",
				Name: "hints",
			})
		}
		sections := []codegen.Section{
			codegen.Header(ts.Name+" agent tools", ts.AgentToolsPackage, imports),
			agentToolsSection(data),
		}
		path := filepath.Join(ts.AgentToolsDir, "helpers.go")
		files = append(files, &codegen.File{Path: path, Sections: sections})
	}
	return files, nil
}

// agentToolsConsumerFiles emits thin helpers in the consumer agent package that
// delegate to provider-side agenttools.NewRegistration helpers for toolsets
// exported by other agents. These helpers improve ergonomics for the agent-as-tool
// pattern without hard-coding aggregators or prompts in the generator.
func agentToolsConsumerFiles(agent *AgentData) []*codegen.File {
	if len(agent.UsedToolsets) == 0 {
		return nil
	}
	files := make([]*codegen.File, 0, len(agent.UsedToolsets))
	for _, ts := range agent.UsedToolsets {
		// Only emit helpers when the toolset is backed by an exported agent and
		// we have a provider agenttools package to call into.
		if ts.AgentToolsImportPath == "" || len(ts.Tools) == 0 {
			continue
		}
		data := agentToolsetConsumerFileData{
			Agent:         agent,
			Toolset:       ts,
			ProviderAlias: ts.AgentToolsPackage,
		}
		imports := []*codegen.ImportSpec{
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "runtime"},
			{Path: ts.AgentToolsImportPath, Name: ts.AgentToolsPackage},
		}
		sections := []codegen.Section{
			codegen.Header(
				ts.Name+" agent toolset client",
				agent.PackageName,
				imports,
			),
			agentToolsConsumerSection(data),
		}
		path := filepath.Join(agent.Dir, ts.PathName+"_agenttools_client.go")
		files = append(files, &codegen.File{Path: path, Sections: sections})
	}
	return files
}

// mcpExecutorFiles emits per-MCP-backed-toolset MCP executors that adapt runtime
// ToolCallExecutor to an mcpruntime.Caller using generated codecs.
func mcpExecutorFiles(agent *AgentData) []*codegen.File {
	out := make([]*codegen.File, 0, len(agent.AllToolsets))
	seen := make(map[string]struct{}, len(agent.AllToolsets))
	for _, ts := range agent.AllToolsets {
		if ts.Expr == nil || ts.Expr.Provider == nil || ts.Expr.Provider.Kind != agentsExpr.ProviderMCP {
			continue
		}
		path := filepath.Join(ts.Dir, "mcp_executor.go")
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		data := serviceToolsetFileData{PackageName: ts.PackageName, Agent: agent, Toolset: ts}
		imports := []*codegen.ImportSpec{
			{Path: "context"},
			{Name: "json", Path: "encoding/json/v2"},
			{Name: "jsontext", Path: "encoding/json/jsontext"},
			{Path: "strings"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "runtime"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp", Name: "mcpruntime"},
			// Per-toolset specs package (codecs + schemas)
			{Path: ts.SpecsImportPath, Name: ts.SpecsPackageName},
		}
		sections := []codegen.Section{
			codegen.Header(ts.Name+" MCP executor", ts.PackageName, imports),
			mcpExecutorSection(data),
		}
		out = append(out, &codegen.File{Path: path, Sections: sections})
	}
	return out
}

// usedToolsFiles emits typed call builders and type aliases for method-backed Used toolsets
// to align UX with agent-as-tool helpers.
func usedToolsFiles(agent *AgentData, specsCache *toolSpecsDataCache) ([]*codegen.File, error) {
	if len(agent.MethodBackedToolsets) == 0 {
		return nil, nil
	}
	files := make([]*codegen.File, 0, len(agent.MethodBackedToolsets))
	for _, ts := range agent.MethodBackedToolsets {
		// Only emit when specs are present
		if ts.SpecsImportPath == "" || len(ts.Tools) == 0 {
			continue
		}
		specs, err := specsCache.specsForToolset(agent.Genpkg, ts)
		if err != nil {
			return nil, fmt.Errorf("agent codegen: build used toolset specs for agent %q toolset %q: %w", agent.Name, ts.QualifiedName, err)
		}
		if specs == nil {
			continue
		}
		data := agentToolsetFileData{PackageName: ts.PackageName, Toolset: ts, Tools: specs.tools}
		imports := []*codegen.ImportSpec{
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
			// Per-toolset specs package for typed payloads
			{Path: ts.SpecsImportPath, Name: ts.SpecsPackageName + "specs"},
		}
		sections := []codegen.Section{
			codegen.Header(ts.Name+" used tool helpers", ts.PackageName, imports),
			usedToolsSection(data),
		}
		path := filepath.Join(ts.Dir, "used_tools.go")
		files = append(files, &codegen.File{Path: path, Sections: sections})
	}
	return files, nil
}

// serviceExecutorFiles emits per-toolset service executors that adapt runtime
// ToolCallExecutor to user-provided callers using generated codecs and optional mappers.
func serviceExecutorFiles(agent *AgentData) []*codegen.File {
	if len(agent.MethodBackedToolsets) == 0 {
		return nil
	}
	files := make([]*codegen.File, 0, len(agent.MethodBackedToolsets))
	for _, ts := range agent.MethodBackedToolsets {
		if ts.Expr == nil || len(ts.Tools) == 0 {
			continue
		}
		svc := ts.SourceService
		if svc == nil {
			svc = agent.Service
		}
		// Use a NameScope to guarantee unique import aliases for the service client
		// and specs packages within this file (for example, service "todos" with
		// toolset "todos"). Keep the service alias derived from the original
		// package name so precomputed method type references remain valid and
		// assign a distinct alias to the specs package when needed.
		aliasScope := codegen.NewNameScope()
		svcAlias := ""
		if svc != nil {
			svcAlias = aliasScope.Unique(servicePkgAlias(svc))
		}
		specsAlias := aliasScope.Unique(ts.SpecsPackageName, "specs")
		// Gather additional imports required by method payload/result types so
		// that type assertions in the executor (for example, args.(*types.Foo))
		// compile even when the payload/result types live in external packages
		// such as the shared gen/types module.
		extraImports := make(map[string]*codegen.ImportSpec)
		for _, t := range ts.Tools {
			if !t.IsMethodBacked {
				continue
			}
			for _, im := range shared.GatherAttributeImports(agent.Genpkg, t.MethodPayloadAttr) {
				if im != nil && im.Path != "" {
					extraImports[im.Path] = im
				}
			}
			for _, im := range shared.GatherAttributeImports(agent.Genpkg, t.MethodResultAttr) {
				if im != nil && im.Path != "" {
					extraImports[im.Path] = im
				}
			}
			for _, sd := range t.ServerData {
				if sd == nil || sd.Schema == nil || sd.Schema.Type == nil {
					continue
				}
				for _, im := range shared.GatherAttributeImports(agent.Genpkg, sd.Schema) {
					if im != nil && im.Path != "" {
						extraImports[im.Path] = im
					}
				}
			}
		}

		// Use a local copy of the toolset so we can override the SpecsPackageName
		// alias for this file without affecting other generated artifacts.
		tsCopy := *ts
		tsCopy.SpecsPackageName = specsAlias

		data := serviceToolsetFileData{
			PackageName:     ts.PackageName,
			Agent:           agent,
			Toolset:         &tsCopy,
			ServicePkgAlias: svcAlias,
		}
		needsSharedTypes := false
		for _, t := range ts.Tools {
			if t == nil || !t.IsMethodBacked {
				continue
			}
			if strings.Contains(t.MethodPayloadTypeRef, "types.") || strings.Contains(t.MethodResultTypeRef, "types.") {
				needsSharedTypes = true
				break
			}
		}
		imports := []*codegen.ImportSpec{
			{Path: "context"},
			{Name: "json", Path: "encoding/json/v2"},
			{Name: "jsontext", Path: "encoding/json/jsontext"},
			{Path: "fmt"},
			{Path: "strings"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime", Name: "runtime"},
			{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"},
			{Path: ts.SpecsImportPath, Name: specsAlias},
		}
		if needsSharedTypes {
			typesPath := filepath.ToSlash(filepath.Join(agent.Genpkg, "types"))
			imports = append(imports, &codegen.ImportSpec{Path: typesPath})
			delete(extraImports, typesPath)
		}
		if svc != nil {
			// Import the service client package (e.g. gen/atlas_data)
			clientPath := filepath.Join(agent.Genpkg, svc.PathName)
			// Check for slash/backslash issues if Genpkg has slashes
			clientPath = strings.ReplaceAll(clientPath, "\\", "/")
			imports = append(imports, &codegen.ImportSpec{Path: clientPath, Name: svcAlias})
			// Avoid duplicating the client import when also discovered via
			// gatherAttributeImports on method payload/result types.
			delete(extraImports, clientPath)
		}
		// Append any remaining external imports needed for payload/result
		// types (for example, the shared gen/types package).
		for _, im := range extraImports {
			if im == nil || im.Path == "" {
				continue
			}
			// Specs and service client imports are already in the list.
			if im.Path == ts.SpecsImportPath {
				continue
			}
			imports = append(imports, im)
		}
		sections := []codegen.Section{
			codegen.Header(ts.Name+" service executor", ts.PackageName, imports),
			serviceExecutorSection(data),
		}
		path := filepath.Join(ts.Dir, "service_executor.go")
		files = append(files, &codegen.File{Path: path, Sections: sections})
	}
	return files
}

// Note: we intentionally avoid parsing type references to infer imports. All
// needed user type imports come from Goa's UserTypeLocation captured in ToolData.

// toolsetHasHintTemplates reports whether any tool in the toolset defines a
// DSL-authored call or result hint template.
func toolsetHasHintTemplates(ts *ToolsetData) bool {
	for _, tool := range ts.Tools {
		if tool.CallHintTemplate != "" || tool.ResultHintTemplate != "" {
			return true
		}
	}
	return false
}
