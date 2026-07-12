package framework

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupTestArtifactsRemovesAndResetsCaches(t *testing.T) {
	preparedRoot := t.TempDir()
	preparedPath := filepath.Join(preparedRoot, "prepared")
	require.NoError(t, os.Mkdir(preparedPath, 0o750))
	binaryPath := filepath.Join(t.TempDir(), "server")
	require.NoError(t, os.WriteFile(binaryPath, []byte("binary"), 0o600))

	codegenMu.Lock()
	preparedExampleCache["fixture"] = preparedExample{root: preparedPath}
	codegenMu.Unlock()
	serverBinMu.Lock()
	serverBinCache["server"] = serverBinaryBuild{path: binaryPath}
	serverBinMu.Unlock()

	require.NoError(t, CleanupTestArtifacts())
	require.NoDirExists(t, preparedPath)
	require.NoFileExists(t, binaryPath)

	codegenMu.Lock()
	require.Empty(t, preparedExampleCache)
	codegenMu.Unlock()
	serverBinMu.Lock()
	require.Empty(t, serverBinCache)
	serverBinMu.Unlock()
}
