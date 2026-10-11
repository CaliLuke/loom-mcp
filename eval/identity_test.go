package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityGitFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	git := func(cmd *exec.Cmd) string {
		t.Helper()
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	git(exec.CommandContext(t.Context(), "git", "init", "--quiet"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("one"), 0600))
	git(exec.CommandContext(t.Context(), "git", "add", "file"))
	git(exec.CommandContext(t.Context(), "git", "-c", "user.name=Store Test", "-c", "user.email=store@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture"))
	want := git(exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD"))
	// F3: An unchanged file with stale stat data must not refresh the index.
	indexPath := filepath.Join(dir, ".git", "index")
	indexBefore, err := readStoreTestFile(indexPath)
	require.NoError(t, err)
	indexInfo, err := os.Stat(indexPath)
	require.NoError(t, err)
	staleTime := time.Unix(1, 0)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "file"), staleTime, staleTime))
	assert.Equal(t, VCS{System: "git", Revision: want, Origin: "git"}, DetectIdentity(t.Context(), dir).Source)
	indexAfter, err := readStoreTestFile(indexPath)
	require.NoError(t, err)
	afterInfo, err := os.Stat(indexPath)
	require.NoError(t, err)
	assert.Equal(t, indexBefore, indexAfter, "identity detection must not refresh the index")
	assert.True(t, indexInfo.ModTime().Equal(afterInfo.ModTime()), "index modification time changed")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("two"), 0600))
	assert.Equal(t, VCS{System: "git", Revision: want, Modified: true, Origin: "git"}, DetectIdentity(t.Context(), dir).Source)
	assert.Equal(t, "none", detectGit(t.Context(), t.TempDir()).Origin)
	assert.NotEmpty(t, DetectIdentity(t.Context(), dir).Versions.Go)
}

func TestDefaultStoreDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested", "deep")
	require.NoError(t, os.MkdirAll(nested, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0600))
	got, err := DefaultStoreDir(nested)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".loom-evals"), got)
	_, err = DefaultStoreDir(t.TempDir())
	require.Error(t, err)
}
