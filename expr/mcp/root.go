// Package mcp defines the expression types used to represent MCP server
// configuration during Goa design evaluation. These types are populated during
// DSL execution and form the schema used for MCP protocol code generation.
package mcp

import (
	"sort"

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
}

// NewRoot creates a new plugin root expression
func NewRoot() *RootExpr {
	return &RootExpr{
		MCPServers:          make(map[string]*MCPExpr),
		DynamicPrompts:      make(map[string][]*DynamicPromptExpr),
		duplicateMCPServers: make(map[string][]*MCPExpr),
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
	prompts, messages := mcpPromptSets(r.MCPServers)
	walk(prompts)
	walk(messages)
	walk(dynamicPromptSet(r.DynamicPrompts))
}

// Validate enforces MCP root-level invariants.
func (r *RootExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	r.validateDuplicateMCPServers(verr)
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

func (r *RootExpr) validateDuplicateMCPServers(verr *eval.ValidationErrors) {
	for _, service := range sortedServiceNames(r.duplicateMCPServers) {
		duplicates := r.duplicateMCPServers[service]
		for _, duplicate := range duplicates {
			verr.Add(duplicate, "duplicate MCP declaration for service %q; MCP() may only be called once per service", service)
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
	r.MCPServers[svc.Name] = mcp
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
