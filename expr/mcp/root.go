// Package mcp defines the expression types used to represent MCP server
// configuration during Goa design evaluation. These types are populated during
// DSL execution and form the schema used for MCP protocol code generation.
package mcp

import (
	"sort"
	"strings"

	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

// Root is the plugin root instance holding all MCP server configurations.
var Root *RootExpr

func init() {
	Root = NewRoot()
	if err := eval.Register(Root); err != nil {
		panic(err)
	}
}

// RootExpr is the top-level root expression for all MCP server declarations.
type RootExpr struct {
	// MCPServers maps service names to their MCP server configurations.
	MCPServers map[string]*MCPExpr
	// DynamicPrompts maps service names to their dynamic prompt
	// expressions.
	DynamicPrompts      map[string][]*DynamicPromptExpr
	duplicateMCPServers map[string][]*MCPExpr
	pendingSkillDirs    map[string][]*SkillDirectoryExpr
}

// NewRoot creates a new plugin root expression
func NewRoot() *RootExpr {
	return &RootExpr{
		MCPServers:          make(map[string]*MCPExpr),
		DynamicPrompts:      make(map[string][]*DynamicPromptExpr),
		duplicateMCPServers: make(map[string][]*MCPExpr),
		pendingSkillDirs:    make(map[string][]*SkillDirectoryExpr),
	}
}

// EvalName returns the plugin name.
func (r *RootExpr) EvalName() string {
	return "MCP plugin"
}

// DependsOn returns the list of other roots this plugin depends on.
func (r *RootExpr) DependsOn() []eval.Root {
	return []eval.Root{expr.Root}
}

// Packages returns the DSL packages that should be recognized for error
// reporting.
func (r *RootExpr) Packages() []string {
	return []string{"github.com/CaliLuke/loom-mcp/dsl"}
}

// WalkSets exposes the nested expressions to the eval engine.
func (r *RootExpr) WalkSets(walk eval.SetWalker) {
	walk(mcpServersSet(r.MCPServers))
	walk(mcpCapabilitiesSet(r.MCPServers))
	walk(mcpToolsSet(r.MCPServers))
	walk(mcpResourcesSet(r.MCPServers))
	if len(r.pendingSkillDirs) > 0 {
		walk(skillDirectorySet(r.pendingSkillDirs))
	}
	prompts, messages := mcpPromptSets(r.MCPServers)
	walk(prompts)
	walk(messages)
	walk(dynamicPromptSet(r.DynamicPrompts))
}

// Validate enforces MCP root-level invariants.
func (r *RootExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	r.validateDuplicateMCPServers(verr)
	r.validatePromptNameCollisions(verr)
	r.validatePendingSkillDirectories(verr)
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

func (r *RootExpr) validatePendingSkillDirectories(verr *eval.ValidationErrors) {
	for _, service := range sortedServiceNames(r.pendingSkillDirs) {
		for _, dir := range r.pendingSkillDirs[service] {
			verr.Add(dir, "SkillDirectory requires MCP to be declared in service %q", service)
		}
	}
}

func (r *RootExpr) validateDuplicateMCPServers(verr *eval.ValidationErrors) {
	for _, service := range sortedServiceNames(r.duplicateMCPServers) {
		duplicates := r.duplicateMCPServers[service]
		for _, duplicate := range duplicates {
			verr.Add(duplicate, "duplicate MCP declaration for service %q; MCP() may only be called once per service", service)
		}
	}
}

func (r *RootExpr) validatePromptNameCollisions(verr *eval.ValidationErrors) {
	for _, service := range sortedPromptServiceNames(r.MCPServers, r.DynamicPrompts) {
		seen := make(map[string]eval.Expression)
		if m := r.MCPServers[service]; m != nil {
			for _, prompt := range m.Prompts {
				if prompt == nil || strings.TrimSpace(prompt.Name) == "" {
					continue
				}
				if _, exists := seen[prompt.Name]; !exists {
					seen[prompt.Name] = prompt
				}
			}
		}
		for _, prompt := range r.DynamicPrompts[service] {
			if prompt == nil || strings.TrimSpace(prompt.Name) == "" {
				continue
			}
			if other, exists := seen[prompt.Name]; exists {
				verr.Add(prompt, "dynamic prompt name %q for service %q duplicates %s", prompt.Name, service, other.EvalName())
				continue
			}
			seen[prompt.Name] = prompt
		}
	}
}

func mcpServersSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	set := make(eval.ExpressionSet, 0, len(servers))
	for _, service := range sortedServiceNames(servers) {
		set = append(set, servers[service])
	}
	return set
}

func mcpCapabilitiesSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, service := range sortedServiceNames(servers) {
		m := servers[service]
		if m.Capabilities != nil {
			set = append(set, m.Capabilities)
		}
	}
	return set
}

func skillDirectorySet(pending map[string][]*SkillDirectoryExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, service := range sortedServiceNames(pending) {
		for _, dir := range pending[service] {
			set = append(set, dir)
		}
	}
	return set
}

func mcpToolsSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, service := range sortedServiceNames(servers) {
		m := servers[service]
		for _, t := range m.Tools {
			set = append(set, t)
		}
	}
	return set
}

func mcpResourcesSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, service := range sortedServiceNames(servers) {
		m := servers[service]
		for _, rsrc := range m.Resources {
			set = append(set, rsrc)
		}
	}
	return set
}

func mcpPromptSets(servers map[string]*MCPExpr) (eval.ExpressionSet, eval.ExpressionSet) {
	var prompts eval.ExpressionSet
	var messages eval.ExpressionSet
	for _, service := range sortedServiceNames(servers) {
		m := servers[service]
		for _, p := range m.Prompts {
			prompts = append(prompts, p)
			for _, msg := range p.Messages {
				messages = append(messages, msg)
			}
		}
	}
	return prompts, messages
}

func dynamicPromptSet(prompts map[string][]*DynamicPromptExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, service := range sortedServiceNames(prompts) {
		ps := prompts[service]
		for _, p := range ps {
			set = append(set, p)
		}
	}
	return set
}

func sortedServiceNames[V any](items map[string]V) []string {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedPromptServiceNames(servers map[string]*MCPExpr, prompts map[string][]*DynamicPromptExpr) []string {
	services := make(map[string]struct{}, len(servers)+len(prompts))
	for service := range servers {
		services[service] = struct{}{}
	}
	for service := range prompts {
		services[service] = struct{}{}
	}
	return sortedServiceNames(services)
}

// RegisterMCP registers an MCP server configuration for a service
func (r *RootExpr) RegisterMCP(svc *expr.ServiceExpr, mcp *MCPExpr) {
	mcp.Service = svc
	if r.MCPServers == nil {
		r.MCPServers = make(map[string]*MCPExpr)
	}
	if _, exists := r.MCPServers[svc.Name]; exists {
		if r.duplicateMCPServers == nil {
			r.duplicateMCPServers = make(map[string][]*MCPExpr)
		}
		r.duplicateMCPServers[svc.Name] = append(r.duplicateMCPServers[svc.Name], mcp)
		return
	}
	mcp.SkillDirectories = append(mcp.SkillDirectories, r.pendingSkillDirs[svc.Name]...)
	delete(r.pendingSkillDirs, svc.Name)
	r.MCPServers[svc.Name] = mcp
}

// DeferSkillDirectory records a service skill directory until MCP is registered.
func (r *RootExpr) DeferSkillDirectory(svc *expr.ServiceExpr, dir *SkillDirectoryExpr) {
	if r.pendingSkillDirs == nil {
		r.pendingSkillDirs = make(map[string][]*SkillDirectoryExpr)
	}
	r.pendingSkillDirs[svc.Name] = append(r.pendingSkillDirs[svc.Name], dir)
}

// ConsumeDeferredSkillDirectory removes dir from the service's pending list.
func (r *RootExpr) ConsumeDeferredSkillDirectory(svc *expr.ServiceExpr, dir *SkillDirectoryExpr) bool {
	pending := r.pendingSkillDirs[svc.Name]
	for i, candidate := range pending {
		if candidate != dir {
			continue
		}
		pending = append(pending[:i], pending[i+1:]...)
		if len(pending) == 0 {
			delete(r.pendingSkillDirs, svc.Name)
		} else {
			r.pendingSkillDirs[svc.Name] = pending
		}
		return true
	}
	return false
}

// RegisterDynamicPrompt registers a dynamic prompt for a service
func (r *RootExpr) RegisterDynamicPrompt(svc *expr.ServiceExpr, prompt *DynamicPromptExpr) {
	r.DynamicPrompts[svc.Name] = append(r.DynamicPrompts[svc.Name], prompt)
}

// GetMCP returns the MCP configuration for a service.
func (r *RootExpr) GetMCP(svc *expr.ServiceExpr) *MCPExpr {
	return r.MCPServers[svc.Name]
}

// ServiceMCP returns the MCP configuration for a service name and optional
// toolset (server name) filter. When toolset is empty, it returns the MCP
// server for the service if present.
func (r *RootExpr) ServiceMCP(service, toolset string) *MCPExpr {
	m, ok := r.MCPServers[service]
	if !ok {
		return nil
	}
	if toolset != "" && m.Name != toolset {
		return nil
	}
	return m
}

// HasMCP returns true if the service has an MCP configuration.
func (r *RootExpr) HasMCP(svc *expr.ServiceExpr) bool {
	_, ok := r.MCPServers[svc.Name]
	return ok
}
