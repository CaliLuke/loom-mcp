// Package mcp defines the expression types used to represent MCP server
// configuration during Goa design evaluation. These types are populated during
// DSL execution and form the schema used for MCP protocol code generation.
package mcp

import (
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
	DynamicPrompts map[string][]*DynamicPromptExpr
}

// NewRoot creates a new plugin root expression
func NewRoot() *RootExpr {
	return &RootExpr{
		MCPServers:     make(map[string]*MCPExpr),
		DynamicPrompts: make(map[string][]*DynamicPromptExpr),
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

func mcpServersSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	set := make(eval.ExpressionSet, 0, len(servers))
	for _, mcp := range servers {
		set = append(set, mcp)
	}
	return set
}

func mcpCapabilitiesSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, m := range servers {
		if m.Capabilities != nil {
			set = append(set, m.Capabilities)
		}
	}
	return set
}

func mcpToolsSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, m := range servers {
		for _, t := range m.Tools {
			set = append(set, t)
		}
	}
	return set
}

func mcpResourcesSet(servers map[string]*MCPExpr) eval.ExpressionSet {
	var set eval.ExpressionSet
	for _, m := range servers {
		for _, rsrc := range m.Resources {
			set = append(set, rsrc)
		}
	}
	return set
}

func mcpPromptSets(servers map[string]*MCPExpr) (eval.ExpressionSet, eval.ExpressionSet) {
	var prompts eval.ExpressionSet
	var messages eval.ExpressionSet
	for _, m := range servers {
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
	for _, ps := range prompts {
		for _, p := range ps {
			set = append(set, p)
		}
	}
	return set
}

// RegisterMCP registers an MCP server configuration for a service
func (r *RootExpr) RegisterMCP(svc *expr.ServiceExpr, mcp *MCPExpr) {
	mcp.Service = svc
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
