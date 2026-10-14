package scenarios_test

import (
	"testing"

	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScenarioContextPreservesBaseline(t *testing.T) {
	for _, v := range scenarios.Variants() {
		t.Run(v.ID, func(t *testing.T) {
			in := &api.RunInput{Policy: v.Policy}
			original := in.Policy.Context
			scenarios.ScenarioContext{PretendWindow: 8000, RecallDiscoveryCalls: 3, RecallReadCalls: 3}.Apply()(in)
			require.NotNil(t, in.Policy.Context.Threshold)
			if v.ID == "baseline" {
				assert.False(t, *in.Policy.Context.Threshold)
				assert.Nil(t, in.Policy.Context.ContextWindow)
				assert.Same(t, original, in.Policy.Context)
			} else {
				require.NotNil(t, in.Policy.Context.ContextWindow)
				assert.Equal(t, 8000, *in.Policy.Context.ContextWindow)
				assert.Nil(t, original.ContextWindow)
				assert.Nil(t, in.Policy.Context.ProtectRecentToolTokens)
				assert.Nil(t, original.ProtectRecentToolTokens)
				assert.NotSame(t, original, in.Policy.Context)
			}
		})
	}
}
