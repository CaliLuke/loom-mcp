package codegen

import agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"

func isMCPBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderMCP
}

func isSkillsBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderSkills
}

func needsExecutorBackedRegistration(ts *ToolsetData) bool {
	return ts != nil && !isMCPBackedToolset(ts) && !isSkillsBackedToolset(ts) && ts.AgentToolsImportPath == ""
}

func needsRuntimeBackedRegistration(ts *ToolsetData) bool {
	return isSkillsBackedToolset(ts)
}

func needsUsedToolsetRegistration(ts *ToolsetData) bool {
	return needsExecutorBackedRegistration(ts) || needsRuntimeBackedRegistration(ts)
}
