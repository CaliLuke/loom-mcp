package env_test

import (
	"encoding/json"
	"testing"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaterializeDeterministicAndPartitions(t *testing.T) {
	pools, err := env.Generate(19)
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, scenario := range env.ScenarioIDs("offline") {
		for partition, count := range map[string]int{"pilot": 6, "look1": 12, "look2": 12, "look3": 24, "smoke": 3} {
			items := pools[scenario+"/"+partition]
			if scenario == "model_change" && partition == "pilot" {
				continue
			}
			require.Len(t, items, count)
			templates := map[string]bool{}
			for _, item := range items {
				assert.False(t, seen[item.ID], item.ID)
				seen[item.ID] = true
				var p env.Parameters
				require.NoError(t, json.Unmarshal(item.Data, &p))
				templates[p.Template] = true
				a, err := env.Materialize(item)
				require.NoError(t, err)
				b, err := env.Materialize(item)
				require.NoError(t, err)
				assert.Equal(t, a, b)
				assert.Less(t, a.TranscriptBytes, 128000*4)
				require.NoError(t, eval.Items(item).Err())
			}
			assert.Len(t, templates, 3)
		}
	}
}
