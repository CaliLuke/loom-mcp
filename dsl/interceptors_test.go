package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestInterceptorsDSL(t *testing.T) {
	runDSL(t, func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("assistant", "Assistant", func() {
				RunPolicy(func() {
					Interceptors("audit", "safety")
				})
			})
		})
	})

	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.Equal(t, []string{"audit", "safety"}, policy.Interceptors)
}
