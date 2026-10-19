package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// RunPolicy emitted into registry registration.
func TestGolden_RunPolicy(t *testing.T) {
	design := testscenarios.RunPolicyBasic()
	files := buildAndGenerate(t, design)
	reg := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")
	require.Contains(t, reg, "Specs: specs.Specs")
	require.Contains(t, reg, "InterruptsAllowed")
	require.Contains(t, reg, "Interceptors: []agentsruntime.Interceptor{")
	require.Contains(t, reg, `NamedInterceptors: []string{"audit", "safety"}`)
	require.Contains(t, reg, "agentsruntime.NewRetryAndReflectInterceptor(agentsruntime.RetryAndReflectConfig{")
	require.Contains(t, reg, "MaxRetries: 2")
	require.Contains(t, reg, "ErrorIfRetryExceeded: true")
	cfg := fileContent(t, files, "gen/alpha/agents/scribe/config.go")
	require.NotContains(t, cfg, "HistoryModel")
	require.NotContains(t, reg, "History:")
	require.Contains(t, cfg, "ContextModel *agentsruntime.ContextModel")
	require.Contains(t, reg, "AfterSystem: true")
	require.Contains(t, reg, "AfterTools: true")
	require.Contains(t, reg, "MaxModelCalls: 7")
	require.Contains(t, reg, "MaxToolCalls: 5")
	require.Contains(t, reg, "MaxRecoveryTurns: 2")
	require.Contains(t, reg, "TimeBudget: time.Duration(30000000000)")
}
