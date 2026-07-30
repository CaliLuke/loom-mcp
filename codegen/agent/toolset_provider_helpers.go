package codegen

import agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"

func isMCPBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderMCP
}

func isSkillsBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderSkills
}

func isArtifactsBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderArtifacts
}

func isMemoryBackedToolset(ts *ToolsetData) bool {
	return ts != nil && ts.Expr != nil && ts.Expr.Provider != nil && ts.Expr.Provider.Kind == agentsExpr.ProviderMemory
}

func needsExecutorBackedRegistration(ts *ToolsetData) bool {
	return ts != nil && !isMCPBackedToolset(ts) && !isSkillsBackedToolset(ts) && !isArtifactsBackedToolset(ts) && !isMemoryBackedToolset(ts) && ts.AgentToolsImportPath == ""
}

func needsRuntimeBackedRegistration(ts *ToolsetData) bool {
	return isSkillsBackedToolset(ts) || isArtifactsBackedToolset(ts) || isMemoryBackedToolset(ts)
}

func needsUsedToolsetRegistration(ts *ToolsetData) bool {
	return needsExecutorBackedRegistration(ts) || needsRuntimeBackedRegistration(ts)
}
