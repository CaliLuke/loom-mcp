package codegen

import (
	"testing"

	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/stretchr/testify/require"
)

func TestNewRunPolicyDataMaterializesDefaultRecoveryTurns(t *testing.T) {
	t.Parallel()

	require.Equal(t, policy.DefaultMaxRecoveryTurns, newRunPolicyData(nil).Caps.MaxRecoveryTurns)
	require.Equal(t, policy.DefaultMaxRecoveryTurns, newRunPolicyData(&agentsExpr.RunPolicyExpr{}).Caps.MaxRecoveryTurns)
	require.Equal(t, policy.DefaultMaxRecoveryTurns, newRunPolicyData(&agentsExpr.RunPolicyExpr{
		DefaultCaps: &agentsExpr.CapsExpr{MaxToolCalls: 4},
	}).Caps.MaxRecoveryTurns)
}
