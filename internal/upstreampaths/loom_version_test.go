package upstreampaths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	remoteVersionPattern = regexp.MustCompile(`(?m)^REMOTE_VERSION="(v\d+\.\d+\.\d+)"$`)
	moduleVersionPattern = regexp.MustCompile(`(?m)^\s*github\.com/CaliLuke/loom (v\d+\.\d+\.\d+)\s*$`)
	adviceVersionPattern = regexp.MustCompile(`github\.com/CaliLuke/loom(?:/cmd/loom)?(?:@| )(v\d+\.\d+\.\d+)`)
)

func TestLoomVersionReferencesStayAligned(t *testing.T) {
	repoRoot := loomMCPRepoRoot(t)
	want := remoteLoomVersion(t, repoRoot)

	for _, name := range []string{
		"go.mod",
		"quickstart/go.mod",
		"integration_tests/fixtures/assistant/go.mod",
		"integration_tests/fixtures/agent_features/go.mod",
		"integration_tests/framework/mcp_dynamic_prompt_example_test.go",
	} {
		data := readRepoFile(t, repoRoot, name)
		match := moduleVersionPattern.FindStringSubmatch(string(data))
		require.Len(t, match, 2, "%s must pin %s", name, LoomCoreModule)
		require.Equal(t, want, match[1], "%s Loom version", name)
	}

	for _, name := range []string{
		"registry/gen/loom.json",
		"quickstart/gen/loom.json",
		"integration_tests/fixtures/assistant/gen/loom.json",
		"integration_tests/fixtures/agent_features/gen/loom.json",
	} {
		var marker struct {
			Version string `json:"loom_version"`
		}
		require.NoError(t, json.Unmarshal(readRepoFile(t, repoRoot, name), &marker), name)
		require.Equal(t, want, marker.Version, "%s generated version", name)
	}

	for _, root := range []string{"README.md", "quickstart/README.md", "docs", ".agents/skills/loom-mcp"} {
		assertAdviceVersions(t, repoRoot, root, want)
	}
}

func assertAdviceVersions(t *testing.T, repoRoot, name, want string) {
	t.Helper()
	path := filepath.Join(repoRoot, name)
	info, err := os.Stat(path)
	require.NoError(t, err)
	if !info.IsDir() {
		assertAdviceFileVersions(t, path, want)
		return
	}
	require.NoError(t, filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		assertAdviceFileVersions(t, path, want)
		return nil
	}))
}

func assertAdviceFileVersions(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from fixed repo documentation roots.
	require.NoError(t, err)
	for _, match := range adviceVersionPattern.FindAllStringSubmatch(string(data), -1) {
		require.Equal(t, want, match[1], "%s Loom advice", path)
	}
}

func remoteLoomVersion(t *testing.T, repoRoot string) string {
	t.Helper()
	data := readRepoFile(t, repoRoot, "scripts/loom_core_mode.sh")
	match := remoteVersionPattern.FindStringSubmatch(string(data))
	require.Len(t, match, 2, "scripts/loom_core_mode.sh must define REMOTE_VERSION")
	return match[1]
}

func readRepoFile(t *testing.T, repoRoot, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, name)) // #nosec G304 -- name comes from fixed test inputs.
	require.NoError(t, err)
	return data
}

func loomMCPRepoRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
