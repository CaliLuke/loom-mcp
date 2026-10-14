package suite

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/require"
)

func TestK19cContractFreezeBindsCalibratedPolicy(t *testing.T) {
	f, evidence := frozenEvidenceFixture(t)
	hash, err := f.Hash()
	require.NoError(t, err)
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	require.NoError(t, err)
	f.Policy.Bootstrap.Seed++
	hash, err = f.Hash()
	require.NoError(t, err)
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	require.Error(t, err, "resealing a freeze cannot change the calibrated bootstrap seed")
}

// This synthetic evidence exercises the pure authorization boundary. It is not
// persisted, source-verified, or represented as measured calibration.
func frozenEvidenceFixture(t *testing.T) (gate.Freeze, freezeEvidence) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	v := validationFixture(t, dir)
	a, source := calibrationFixture(t)
	source.BaseRevision = strings.Repeat("a", 40)
	in, pools, err := Inputs(dir)
	require.NoError(t, err)
	in.SourceSHA256, in.SourceBundleSHA256 = source.Identity.SHA256, source.BundleSHA256
	in.ValidationSHA256, in.InventorySHA256 = a.ValidationSHA256, strings.Repeat("b", 64)
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	in.CalibrationSHA256 = gate.Digest(raw)
	in.Roles, in.Profiles, in.EffectivePolicies = v.Profile.Roles, freezeProfiles(v.Profile), v.EffectivePolicies
	evidence := freezeEvidence{source: source, validation: v, calibration: a, inputs: in, pools: pools}
	f, err := buildEvidenceFreeze("offline", evidence)
	require.NoError(t, err)
	return f, evidence
}
