package release

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv("RELEASE_FAKE_GH") == "1" {
		os.Exit(runFakeGH())
	}
	os.Exit(m.Run())
}

func runFakeGH() int {
	args := []string{"workflow", "run", "release.yml", "--repo", repository, "--ref", branchMain, "--json"}
	if !slices.Equal(os.Args[1:], args) {
		return 1
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	expected := `{"mode":"alpha","source":"` + os.Getenv("RELEASE_EXPECTED_SOURCE") + `","alpha":"","version":""}`
	if string(payload) != expected {
		return 1
	}
	return 0
}

func TestWorkflowPublicationContract(t *testing.T) {
	releaseWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	require.NoError(t, err)
	release := string(releaseWorkflow)
	require.Contains(t, release, "workflow_dispatch:")
	require.Contains(t, release, "schedule:")
	require.Contains(t, release, "timezone: America/Los_Angeles")
	require.Contains(t, release, "group: loom-mcp-publication")
	require.Contains(t, release, "if: github.ref == 'refs/heads/main'")
	require.NotContains(t, release, "\n  push:")
	require.NotContains(t, release, "\n  pull_request:")

	ciWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	require.NoError(t, err)
	ci := string(ciWorkflow)
	require.Contains(t, ci, "name: Release eligibility")
	require.Contains(t, ci, "needs: [verify]")
	require.Contains(t, ci, "all(.[]; .result == \"success\")")
}

func TestDispatchTargetsTrustedMainAndExactSource(t *testing.T) {
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, gh))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RELEASE_FAKE_GH", "1")
	t.Setenv("RELEASE_EXPECTED_SOURCE", sha)
	config := Config{Mode: "alpha", Source: sha}
	require.NoError(t, Dispatch(context.Background(), config))
	payload, err := dispatchPayload(config)
	require.NoError(t, err)
	require.JSONEq(t, `{"mode":"alpha","source":"`+sha+`","alpha":"","version":""}`, string(payload))
	require.Equal(t, []string{"workflow", "run", "release.yml", "--repo", repository, "--ref", "main", "--json"}, dispatchArgs())
}

func TestDispatchRejectsInvalidInputsBeforeExecutingGH(t *testing.T) {
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, gh))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RELEASE_FAKE_GH", "1")
	t.Setenv("RELEASE_EXPECTED_SOURCE", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, tc := range []struct {
		config  Config
		message string
	}{
		{Config{Mode: "alpha", Source: "main"}, "alpha requires a full source SHA"},
		{Config{Mode: "promote", Alpha: "v2.1.0-alpha.1", Version: "v2.2.0"}, "promotion must map"},
	} {
		err := Dispatch(context.Background(), tc.config)
		require.ErrorContains(t, err, tc.message)
	}
}
