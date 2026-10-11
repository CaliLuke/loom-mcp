package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/assert"
)

// TestGolden_Context proves literals and conditional generation for every Context branch.
func TestGolden_Context(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ContextPolicies())
	reg := fileContent(t, files, "gen/alpha/agents/compactor/registry.go")
	cfg := fileContent(t, files, "gen/alpha/agents/compactor/config.go")
	specs := fileContent(t, files, "gen/alpha/toolsets/evidence/specs.go")
	assert.Contains(t, reg, "ContextCompaction: &agentsruntime.ContextCompaction{")
	assert.Contains(t, reg, "Tokens: 85, Bytes: 100")
	assert.Contains(t, reg, "KeepRecentTokens: 8000")
	assert.Contains(t, reg, "ContextModel: cfg.ContextModel")
	assert.Contains(t, reg, "DisableRecall: true")
	assert.Contains(t, cfg, "ContextModel *agentsruntime.ContextModel")
	assert.Contains(t, cfg, "context model is required when Summarize is configured")
	assert.Contains(t, specs, "KeepResultInContext: true")
	assertGoldenGo(t, "context", "registry.go.golden", reg)
	assertGoldenGo(t, "context", "config.go.golden", cfg)
	assertGoldenGo(t, "context", "specs.go.golden", specs)
	disabled := fileContent(t, files, "gen/alpha/agents/disabled/registry.go")
	disabledCfg := fileContent(t, files, "gen/alpha/agents/disabled/config.go")
	assert.NotContains(t, disabled, "ContextCompaction:")
	assert.Contains(t, disabled, "DisableRecall: true")
	assert.NotContains(t, disabledCfg, "ContextModel")
	assertGoldenGo(t, "context", "disabled_registry.go.golden", disabled)
	defaults := fileContent(t, files, "gen/alpha/agents/defaults/registry.go")
	defaultCfg := fileContent(t, files, "gen/alpha/agents/defaults/config.go")
	assert.Contains(t, defaults, "ContextCompaction:")
	assert.NotContains(t, defaults, "TriggerAt:")
	assert.NotContains(t, defaults, "ContextRecovery:")
	assert.Contains(t, defaultCfg, "ContextModel *agentsruntime.ContextModel")
	assert.NotContains(t, defaultCfg, "context model is required")
}
