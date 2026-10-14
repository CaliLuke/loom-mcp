package suite

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCalibrationDriftRefusesReport(t *testing.T) {
	f, evidence := frozenEvidenceFixture(t)
	hash, err := f.Hash()
	require.NoError(t, err)
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	require.NoError(t, err)
	evidence.inputs.CalibrationSHA256 = strings.Repeat("f", 64)
	_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
	require.Error(t, err)
}
