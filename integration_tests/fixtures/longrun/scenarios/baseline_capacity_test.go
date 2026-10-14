package scenarios_test

import (
	"testing"

	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
)

func TestK19cContractBaselineRequestCapacity(t *testing.T) {
	for _, tokens := range []int{127999, 128000, 128001} {
		record := &runrecord.RunRecord{Runs: []runrecord.Run{{WorkflowProtocolVersion: api.WorkflowProtocolVersion, Calls: []runrecord.Call{{Completion: &callrecord.Completion{Usage: model.TokenUsage{InputTokens: tokens}}}}}}}
		err := scenarios.CheckStructure("fact_early", "baseline", record)
		if tokens < 128000 {
			assert.NoError(t, err)
		} else {
			assert.ErrorContains(t, err, "128000", "actual baseline request must fit within128k")
		}
	}
}
