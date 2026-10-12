package env_test

import (
	"strings"
	"testing"

	"example.com/longrun/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPressureNeedsAndRecallEvidence(t *testing.T) {
	pools, err := env.Generate(20261022)
	require.NoError(t, err)
	for _, item := range pools["recall_pressure/smoke"] {
		m, err := env.Materialize(item)
		require.NoError(t, err)
		require.Len(t, m.Spec.Needs, 2)
		assert.NotContains(t, m.Spec.Needs[0].Trigger, m.Spec.Needs[1].Trigger, "final need must not match the middle need")
		for _, need := range m.Spec.Needs {
			matches := 0
			for _, result := range m.Results {
				if strings.Contains(result, need.Trigger) {
					matches++
				}
			}
			assert.Equal(t, 1, matches, "each need has one original trigger")
		}
		for _, value := range m.Spec.Answer {
			for _, result := range m.Results {
				if at := strings.Index(result, value); at >= 0 {
					assert.Greater(t, at, 8000, "pressure facts must lie beyond the summary prefix for the 8k window")
				}
			}
		}
	}
}
