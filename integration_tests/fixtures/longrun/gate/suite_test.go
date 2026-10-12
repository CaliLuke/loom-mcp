package suite_test

import (
	"os"

	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrozenBoundaries(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 19))
	f, err := suite.BuildFreeze(dir, "offline", []byte("calibration"), eval.VCS{Revision: "test-revision"})
	require.NoError(t, err)
	require.NoError(t, suite.WriteFreeze(dir, f))
	fixtureRoot, err := os.OpenRoot(dir)
	require.NoError(t, err)
	require.NoError(t, fixtureRoot.WriteFile("gate/calibration_default.json", []byte("calibration"), 0600))
	require.NoError(t, fixtureRoot.Close())
	_, err = suite.VerifyFreeze(dir, "offline")
	require.NoError(t, err)
	require.ErrorContains(t, suite.WriteItems(dir, 20), "frozen")
	r, err := os.OpenRoot(dir)
	require.NoError(t, err)
	data, err := r.ReadFile("testdata/pools/fact_early/look1.jsonl")
	require.NoError(t, err)
	require.NoError(t, r.WriteFile("testdata/pools/fact_early/look1.jsonl", append(data, '\n'), 0600))
	require.NoError(t, r.Close())
	_, err = suite.VerifyFreeze(dir, "offline")
	assert.Error(t, err)
}

func TestMissingWholeItemAndLookPrefix(t *testing.T) {
	cfg := gate.Config{Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Scenarios: []string{"fact_early"}, Repeats: 2, Items: map[string][]string{"fact_early": {"missing"}}}
	e, err := gate.Evaluate(nil, cfg, 1)
	require.NoError(t, err)
	d, err := suite.DecideLook(e, nil)
	require.NoError(t, err)
	assert.Equal(t, gate.Incomplete, d.Status)
	assert.Len(t, d.Rerun, 6)
	_, err = suite.DecideLook(gate.Evaluation{Look: 2}, nil)
	require.ErrorContains(t, err, "prior")
	_, err = suite.DecideLook(gate.Evaluation{Look: 2, Variants: make([]gate.VariantResult, 2)}, []gate.Decision{{Look: 1, Status: gate.Fail}})
	assert.ErrorContains(t, err, "refused")
}
