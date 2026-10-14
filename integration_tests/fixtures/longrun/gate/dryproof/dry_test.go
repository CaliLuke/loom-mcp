package dryproof_test

import (
	"encoding/json"
	"os"
	"testing"

	"example.com/longrun/fake"
	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cDryStoredProof(t *testing.T) {
	for _, tc := range []struct {
		name     string
		faults   fake.Faults
		injected bool
	}{
		{"reader", fake.Faults{}, false},
		{"fault", fake.Faults{DropFacts: true, NoRecall: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := suite.DryGate(t.Context(), "../..", tc.faults, 4)
			require.NoError(t, err)
			require.Len(t, result.Evaluations, 1)
			require.Len(t, result.Decisions, 1)
			assert.Equal(t, suite.ProposedOfflinePolicy(), result.Evaluations[0].Policy)
			require.Len(t, result.Decisions[0].Variants, 2)
			assert.Equal(t, result.Decisions[0].Variants, result.Variants)
			for key, diagnostic := range result.Diagnostics {
				assert.True(t, diagnostic.Available, key)
			}
			for _, variant := range result.Decisions[0].Variants {
				assert.NotEqual(t, gate.Incomplete, variant.Status)
				if tc.injected {
					assert.Equal(t, gate.Fail, variant.Status)
				}
			}
			if !tc.injected {
				for key, diagnostic := range result.Diagnostics {
					assert.Equal(t, diagnostic.Attempts, diagnostic.Passed, key)
				}
			}
			// Persist the entire report, then reconstruct decisions from recorded
			// evaluations and their explicit policy without a harness or provider.
			data, err := json.Marshal(result)
			require.NoError(t, err)
			root, err := os.OpenRoot(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, root.WriteFile("dry.json", data, 0600))
			saved, err := root.ReadFile("dry.json")
			require.NoError(t, err)
			require.NoError(t, root.Close())
			var stored suite.Report
			require.NoError(t, json.Unmarshal(saved, &stored))
			decisions := make([]gate.Decision, 0, len(stored.Evaluations))
			for _, evaluation := range stored.Evaluations {
				decision, err := suite.DecideLook(evaluation, decisions)
				require.NoError(t, err)
				decisions = append(decisions, decision)
			}
			assert.Equal(t, result.Decisions, decisions)
			assert.Equal(t, result.Diagnostics, stored.Diagnostics)
		})
	}
}
