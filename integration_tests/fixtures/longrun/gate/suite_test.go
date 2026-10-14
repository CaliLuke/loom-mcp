package suite

import (
	"bytes"
	"os"

	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrozenBoundaries(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	f, evidence := frozenEvidenceFixture(t)
	hash, err := f.Hash()
	require.NoError(t, err)
	require.NoError(t, WriteFreeze(dir, f))
	fixtureRoot, err := os.OpenRoot(dir)
	require.NoError(t, err)
	require.NoError(t, fixtureRoot.WriteFile("gate/calibration_default.json", []byte("calibration"), 0600))
	require.NoError(t, fixtureRoot.Close())
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	require.NoError(t, err)
	require.ErrorContains(t, WriteItems(dir, 20), "frozen")
	r, err := os.OpenRoot(dir)
	require.NoError(t, err)
	data, err := r.ReadFile("testdata/pools/fact_early/look1.jsonl")
	require.NoError(t, err)
	edited := bytes.Replace(data, []byte("Audit"), []byte("Alter"), 1)
	require.NotEqual(t, data, edited)
	require.NoError(t, r.WriteFile("testdata/pools/fact_early/look1.jsonl", edited, 0600))
	require.NoError(t, r.Close())
	changed, _, err := Inputs(dir)
	require.NoError(t, err)
	changed.SourceSHA256, changed.SourceBundleSHA256 = evidence.inputs.SourceSHA256, evidence.inputs.SourceBundleSHA256
	changed.ValidationSHA256, changed.InventorySHA256, changed.CalibrationSHA256 = evidence.inputs.ValidationSHA256, evidence.inputs.InventorySHA256, evidence.inputs.CalibrationSHA256
	changed.Roles, changed.Profiles, changed.EffectivePolicies = evidence.inputs.Roles, evidence.inputs.Profiles, evidence.inputs.EffectivePolicies
	evidence.inputs = changed
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	assert.Error(t, err)
}

func TestMissingWholeItemAndLookPrefix(t *testing.T) {
	cfg := gate.Config{Policy: ProposedOfflinePolicy(), Baseline: "baseline", Variants: []string{"receipt_only", "hybrid"}, Scenarios: []string{"fact_early"}, Repeats: 2, Items: map[string][]string{"fact_early": {"missing"}}}
	e, err := gate.Evaluate(nil, cfg, 1)
	require.NoError(t, err)
	d, err := DecideLook(e, nil)
	require.NoError(t, err)
	assert.Equal(t, gate.Incomplete, d.Status)
	assert.Len(t, d.Rerun, 6)
	_, err = DecideLook(gate.Evaluation{Look: 2}, nil)
	require.ErrorContains(t, err, "prior")
	e.Look = 2
	_, err = DecideLook(e, []gate.Decision{d})
	assert.ErrorContains(t, err, "incomplete")
}
