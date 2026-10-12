package agenttest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type artifactTB struct {
	*evalTB
	dir   string
	calls int
	fail  bool
}

func TestRepeatArtifactDirectories(t *testing.T) {
	parent := &artifactTB{evalTB: &evalTB{TB: t}, dir: t.TempDir()}
	var dirs []string
	report := agenttest.Repeat(parent, 2, 2, func(at testing.TB) {
		assert.Equal(t, 1, parent.calls, "parent ArtifactDir must be resolved once before attempts")
		dir := at.ArtifactDir()
		assert.Equal(t, dir, at.ArtifactDir(), "same directory within an attempt")
		assert.Equal(t, parent.dir, filepath.Dir(dir))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "evidence.txt"), []byte("kept"), 0o600))
		dirs = append(dirs, dir)
	})
	assert.Equal(t, 2, report.Passed)
	require.Len(t, dirs, 2)
	assert.NotEqual(t, dirs[0], dirs[1])
	for _, dir := range dirs {
		root, err := os.OpenRoot(dir)
		require.NoError(t, err)
		body, err := root.ReadFile("evidence.txt")
		assert.NoError(t, root.Close())
		require.NoError(t, err)
		assert.Equal(t, "kept", string(body), "artifact lifetime belongs to the parent")
	}
}

func TestRepeatArtifactFailureStaysInAttempt(t *testing.T) {
	parent := &artifactTB{evalTB: &evalTB{TB: t}, dir: filepath.Join(t.TempDir(), "file")}
	require.NoError(t, os.WriteFile(parent.dir, nil, 0o600))
	attempt := 0
	report := agenttest.Repeat(parent, 2, 1, func(at testing.TB) {
		attempt++
		if attempt == 1 {
			at.ArtifactDir()
		}
	})
	assert.False(t, parent.failed)
	assert.Equal(t, 1, report.Passed)
	require.Len(t, report.Failed, 1)
	assert.Equal(t, 1, report.Failed[0].Index)
	assert.Contains(t, report.Failed[0].Diagnostics[0], "ArtifactDir")
}

func TestRepeatParentArtifactFailureStopsBeforeAttempts(t *testing.T) {
	parent := &artifactTB{evalTB: &evalTB{TB: t}, fail: true}
	attempts := 0
	returned := false
	runTB(func() {
		agenttest.Repeat(parent, 2, 1, func(at testing.TB) {
			attempts++
			at.ArtifactDir()
		})
		returned = true
	})
	assert.True(t, parent.failed)
	assert.Equal(t, 1, parent.calls)
	assert.Zero(t, attempts, "parent failure must occur before starting an attempt")
	assert.False(t, returned, "parent Fatal must abort the Repeat caller")
}

func (t *artifactTB) ArtifactDir() string {
	t.calls++
	if t.fail {
		t.Fatalf("parent ArtifactDir creation failed")
	}
	return t.dir
}
