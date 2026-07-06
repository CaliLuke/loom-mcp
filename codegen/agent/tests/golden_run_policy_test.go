package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

// RunPolicy emitted into registry registration.
func TestGolden_RunPolicy(t *testing.T) {
	design := testscenarios.RunPolicyBasic()
	files := buildAndGenerate(t, design)
	reg := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")
	require.Contains(t, reg, "Specs: specs.Specs")
	require.Contains(t, reg, "InterruptsAllowed")
	require.Contains(t, reg, "agentsruntime.HistoryCompressionConfig")
	require.Contains(t, reg, "CompressAtMaxInputTokens: 120000")
	require.Contains(t, reg, "KeepMaxInputTokens: 40000")
	require.Contains(t, reg, "KeepMaxTurns: 12")
	require.Contains(t, reg, "Interceptors: []agentsruntime.Interceptor{")
	require.Contains(t, reg, "agentsruntime.NewRetryAndReflectInterceptor(agentsruntime.RetryAndReflectConfig{")
	require.Contains(t, reg, "MaxRetries: 2")
	require.Contains(t, reg, "ErrorIfRetryExceeded: true")
	cfg := fileContent(t, files, "gen/alpha/agents/scribe/config.go")
	require.Contains(t, cfg, "HistoryModel model.Client")
	require.Contains(t, reg, "return nil")
}
