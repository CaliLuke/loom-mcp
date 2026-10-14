package suite_test

import (
	"encoding/json"
	"strings"
	"testing"

	"example.com/longrun"
	"example.com/longrun/env"
	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/require"
)

func TestK19cContractPilotRequiresExactManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 19))
	profile, err := longrun.ResolveProfile("offline")
	require.NoError(t, err)
	digest := strings.Repeat("a", 64)
	complete := suite.PilotEvidence{Version: 1, Configuration: "offline", ExperimentID: "019a0c72-0000-7000-8000-000000000001", ExperimentSHA256: digest, SourceSHA256: digest, Profile: profile}
	for _, scenario := range env.GatedScenarios("offline") {
		pool, err := suite.LoadPool(dir, scenario, "pilot")
		require.NoError(t, err)
		for _, item := range pool.Items() {
			for repeat := 1; repeat <= 2; repeat++ {
				complete.Rows = append(complete.Rows, suite.PilotObservation{Key: eval.AttemptKey{Scenario: scenario, Variant: "baseline", Item: item.ID, Repeat: repeat}, Models: map[string]string{"planner": "reader"}})
			}
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*suite.PilotEvidence)
	}{
		{"missing", func(p *suite.PilotEvidence) { p.Rows = p.Rows[1:] }},
		{"duplicate", func(p *suite.PilotEvidence) { p.Rows[0] = p.Rows[1] }},
		{"wrong repeat", func(p *suite.PilotEvidence) { p.Rows[0].Key.Repeat = 3 }},
		{"nonbaseline", func(p *suite.PilotEvidence) { p.Rows[0].Key.Variant = "hybrid" }},
		{"wrong partition", func(p *suite.PilotEvidence) { p.Rows[0].Key.Item = "fact_early.ops_deployer.6" }},
		{"wrong observed role", func(p *suite.PilotEvidence) { p.Rows[0].Models["planner"] = "other" }},
		{"infrastructure", func(p *suite.PilotEvidence) { p.Rows[0].Error = "connection failed" }},
		{"source drift", func(p *suite.PilotEvidence) { p.SourceSHA256 = strings.Repeat("b", 64) }},
		{"missing role", func(p *suite.PilotEvidence) { delete(p.Profile.Roles, "summariser") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(complete)
			require.NoError(t, err)
			var modified suite.PilotEvidence
			require.NoError(t, json.Unmarshal(data, &modified))
			tc.change(&modified)
			_, err = suite.VerifyPilot(dir, "offline", modified, profile, digest)
			require.Error(t, err)
		})
	}
	// Ordinary failed agent outcomes remain observed baseline rates.
	rates, err := suite.VerifyPilot(dir, "offline", complete, profile, digest)
	require.NoError(t, err)
	require.Len(t, rates, 12)
	for _, rate := range rates {
		require.Zero(t, rate.Success)
	}
}
