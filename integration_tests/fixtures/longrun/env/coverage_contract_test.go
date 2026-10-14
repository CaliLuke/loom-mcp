package env_test

import (
	"encoding/json"
	"testing"

	"example.com/longrun/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cContractCoverageMaterialization(t *testing.T) {
	pools, err := env.Generate(19)
	require.NoError(t, err)
	for _, scenario := range env.ScenarioIDs("offline") {
		t.Run(scenario, func(t *testing.T) {
			for _, item := range pools[scenario+"/smoke"] {
				var base env.Parameters
				require.NoError(t, json.Unmarshal(item.Data, &base))
				require.Equal(t, 18, base.Steps, "raw frozen input remains the base workload")
				materialized, err := env.Materialize(item)
				require.NoError(t, err)
				want := base
				want.Steps *= 2
				if scenario == "model_change" {
					want.Steps = base.Steps * 4
				}
				assert.Equal(t, want, materialized.Parameters, "only effective workload length changes")
				assert.Len(t, materialized.Results, len(materialized.Inputs)*want.Steps)
				assert.NotEmpty(t, materialized.Spec.Evidence)
			}
		})
	}
}
