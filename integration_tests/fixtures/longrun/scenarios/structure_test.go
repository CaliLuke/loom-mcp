package scenarios_test

import (
	"testing"

	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
)

func TestStructureRequiresCurrentWorkflowProtocol(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		valid   bool
	}{
		{name: "current", version: api.WorkflowProtocolVersion, valid: true},
		{name: "previous", version: 18},
		{name: "missing", version: 0},
		{name: "future", version: api.WorkflowProtocolVersion + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &runrecord.RunRecord{Runs: []runrecord.Run{{WorkflowProtocolVersion: tc.version}}}
			err := scenarios.CheckStructure("fact_early", "baseline", record)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, "workflow protocol")
			}
		})
	}
}

func TestStructureRejectsPreviousProtocolInChild(t *testing.T) {
	record := &runrecord.RunRecord{Runs: []runrecord.Run{
		{WorkflowProtocolVersion: api.WorkflowProtocolVersion},
		{WorkflowProtocolVersion: 18},
	}}
	assert.ErrorContains(t, scenarios.CheckStructure("fact_early", "baseline", record), "workflow protocol")
}
