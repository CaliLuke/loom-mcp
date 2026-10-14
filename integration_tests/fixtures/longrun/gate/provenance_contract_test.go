package suite_test

import (
	"os"
	"path/filepath"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/stretchr/testify/require"
)

func TestK19cContractProvenanceRefusesBeforeWriting(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1")
	dir := t.TempDir()
	_, err := suite.WriteSource(t.Context(), dir, "offline", "HEAD")
	require.Error(t, err, "native or unbound executable cannot write evidence")
	_, err = os.Stat(filepath.Join(dir, "gate"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestK19cContractReadSourceRejectsAbsentAndUnknown(t *testing.T) {
	for _, configuration := range []string{"offline", "../escape", "unknown"} {
		t.Run(configuration, func(t *testing.T) {
			_, err := suite.ReadSource(t.Context(), t.TempDir(), configuration)
			require.Error(t, err)
		})
	}
}
