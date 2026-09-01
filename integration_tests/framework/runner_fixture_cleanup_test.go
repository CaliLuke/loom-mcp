package framework

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestCleanupStaleExampleRootsRemovesOnlyOldHarnessDirectories(t *testing.T) {
	tmpBase := t.TempDir()
	staleRoot := filepath.Join(tmpBase, exampleRootPrefix+"stale")
	freshRoot := filepath.Join(tmpBase, exampleRootPrefix+"fresh")
	unrelatedRoot := filepath.Join(tmpBase, "unrelated")
	harnessFile := filepath.Join(tmpBase, exampleRootPrefix+"file")
	for _, path := range []string{staleRoot, freshRoot, unrelatedRoot} {
		require.NoError(t, os.Mkdir(path, 0o750))
	}
	require.NoError(t, os.WriteFile(harnessFile, []byte("not a directory"), 0o600))
	now := time.Now()
	staleTime := now.Add(-staleExampleRootAge - time.Hour)
	require.NoError(t, os.Chtimes(staleRoot, staleTime, staleTime))

	require.NoError(t, cleanupStaleExampleRoots(tmpBase, now.Add(-staleExampleRootAge)))

	require.NoDirExists(t, staleRoot)
	require.DirExists(t, freshRoot)
	require.DirExists(t, unrelatedRoot)
	require.FileExists(t, harnessFile)
}
