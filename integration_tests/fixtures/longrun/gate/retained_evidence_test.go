package suite

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This revision produced the retained diagnostic calibration. It has no
// historical-test wrapper, so invoking its artifact test cannot recurse.
const retainedCalibrationRevision = "88eabc2283bc4d790e81cde84348737aae8212ef"

func verifyRetainedCalibration(t *testing.T) {
	t.Helper()
	root, err := EvidenceRoot("..")
	require.NoError(t, err)
	verifyRetainedArtifactBytes(t, root)
	checkout := filepath.Join(t.TempDir(), "retained")
	command := exec.CommandContext(t.Context(), "git", "worktree", "add", "--detach")
	command.Args = append(command.Args, checkout, retainedCalibrationRevision)
	command.Dir = root
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Cleanup(func() {
		// Cleanup must also run after the test context is canceled.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "git", "worktree", "remove", "--force")
		command.Args = append(command.Args, checkout)
		command.Dir = root
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	})
	command = exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "-timeout=30m", "./gate", "-run", "^TestK19cCommittedArtifacts$")
	command.Dir = filepath.Join(checkout, "integration_tests/fixtures/longrun")
	// The retained test builds with race support, then explicitly selects
	// pure-Go source verification using the original recorded build settings.
	command.Env = append(os.Environ(), "CGO_ENABLED=1", "GOFLAGS=-p=2", "GOMAXPROCS=4", "LOOM_LIVE_MODEL=0", "CODEX_INTEGRATION=0")
	output, err = command.CombinedOutput()
	require.NoError(t, err, "retained source artifact verification: %s", output)
	t.Logf("retained calibration %s: %s", retainedCalibrationRevision, output)
}

func verifyRetainedArtifactBytes(t *testing.T, root string) {
	t.Helper()
	const prefix = "integration_tests/fixtures/longrun/"
	currentRoot, err := os.OpenRoot(filepath.Join(root, prefix))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, currentRoot.Close()) })
	command := exec.CommandContext(t.Context(), "git", "ls-tree", "-r", "--name-only", retainedCalibrationRevision, "--", prefix)
	command.Dir = root
	output, err := command.Output()
	require.NoError(t, err)
	var expected []string
	for path := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		path = strings.TrimPrefix(path, prefix)
		require.True(t, filepath.IsLocal(path), "non-local retained path: %s", path)
		if !retainedArtifactPath(path) {
			continue
		}
		expected = append(expected, path)
		command := exec.CommandContext(t.Context(), "git", "show")
		command.Args = append(command.Args, retainedCalibrationRevision+":"+prefix+path)
		command.Dir = root
		original, err := command.Output()
		require.NoError(t, err)
		current, err := currentRoot.ReadFile(path)
		require.NoError(t, err)
		require.True(t, bytes.Equal(original, current), "retained artifact changed: %s", path)
	}
	require.NotEmpty(t, expected)
	var actual []string
	err = fs.WalkDir(currentRoot.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && retainedArtifactPath(path) {
			actual = append(actual, path)
		}
		return nil
	})
	require.NoError(t, err)
	slices.Sort(expected)
	slices.Sort(actual)
	require.Equal(t, expected, actual, "retained evidence inventory changed")
}

func retainedArtifactPath(path string) bool {
	if strings.HasPrefix(path, "testdata/") || strings.HasPrefix(path, "gate/archive/") || strings.HasPrefix(path, "gate/source/") {
		return true
	}
	return filepath.Dir(path) == "gate" && slices.Contains([]string{".json", ".txt", ".sha256"}, filepath.Ext(path))
}
