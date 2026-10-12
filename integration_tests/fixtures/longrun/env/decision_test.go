package env_test

import (
	"encoding/json"
	"testing"

	"example.com/longrun/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecisionHasASeparateProseTurn(t *testing.T) {
	pools, err := env.Generate(19)
	require.NoError(t, err)
	item := pools["decisions_dependencies/smoke"][0]
	m, err := env.Materialize(item)
	require.NoError(t, err)
	assert.Len(t, m.Inputs, 2, "first run ends with a prose decision, second combines three evicted results")
}

func TestDecisionEvidenceCanLeaveCallerHistory(t *testing.T) {
	pools, err := env.Generate(19)
	require.NoError(t, err)
	m, err := env.Materialize(pools["decisions_dependencies/smoke"][0])
	require.NoError(t, err)
	input, err := json.Marshal(m.Inputs)
	require.NoError(t, err)
	assert.NotContains(t, string(input), m.Spec.Answer[0], "the caller history must not permanently pin the decision evidence")
	assert.Contains(t, m.Results["r1.17"], "choice = "+m.Spec.Answer[0], "the last result supplies a grounded choice for the prose decision")
}
