package env_test

import (
	"encoding/json"
	"strings"
	"testing"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLargeResultFitsProjectionWithFactInMiddle(t *testing.T) {
	pools, err := env.Generate(20261021)
	require.NoError(t, err)
	for _, partition := range []string{"pilot", "look1", "look2", "look3", "smoke"} {
		for _, item := range pools["large_result_middle/"+partition] {
			t.Run(item.ID, func(t *testing.T) {
				m, err := env.Materialize(item)
				require.NoError(t, err)
				found := 0
				for _, result := range m.Results {
					at := strings.Index(result, m.Spec.Answer[0])
					if at < 0 {
						continue
					}
					found++
					encoded, err := json.Marshal(result)
					require.NoError(t, err)
					assert.LessOrEqual(t, len(encoded), 60000, "complete result including JSON framing")
					assert.Greater(t, len(result), 55000, "keep the intended large-result workload")
					assert.InDelta(t, .5, float64(at)/float64(len(result)), .01, "fact in the middle")
					projected, err := transcript.ProjectToolResultContent(rawjson.Message(encoded), nil, "", "")
					require.NoError(t, err)
					text, ok := projected.(string)
					require.True(t, ok, "result must survive projection rather than become an omission")
					assert.Equal(t, result, text)
				}
				assert.Equal(t, 1, found)
			})
		}
	}
}
