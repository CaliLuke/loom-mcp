package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationStopsAtFailedExternalOperation(t *testing.T) {
	for _, operation := range []string{
		"remote get-url --all", "remote get-url --push", "fetch origin", "for-each-ref", "merge-base", "git show", "releases?", "/workflows/", "/jobs?", "git log", "git ls-remote", "git tag", "release create", "/releases/tags/", "release download",
	} {
		t.Run(operation, func(t *testing.T) {
			f := newBoundaryPublication()
			fault := errors.New("external operation unavailable")
			failed := false
			p := publisher{config: Config{Mode: modeAlpha, Source: f.sha}, run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				require.False(t, failed, "publication must not continue after a failed external operation")
				if strings.Contains(name+" "+strings.Join(args, " "), operation) {
					failed = true
					return nil, fault
				}
				return f.run(ctx, name, args...)
			}}
			require.ErrorIs(t, p.publish(t.Context()), fault)
			assert.True(t, failed)
		})
	}
}

func TestPublicationRejectsCorruptedAuthorityResponses(t *testing.T) {
	for _, tc := range []struct{ name, operation, response string }{
		{"fetch origin", "remote get-url --all", "https://example.test/other.git"},
		{"tag object", "for-each-ref", "v2.1.0-alpha.1 tree object"},
		{"incomplete annotated tag", "for-each-ref", "v2.1.0-alpha.1 tag object"},
		{"train JSON", "git show", "{"},
		{"train version", "git show", `{"version":"v1.0.0"}`},
		{"release listing", "releases?", "{"},
		{"CI listing", "/workflows/", "{"},
		{"jobs listing", "/jobs?", "{"},
		{"missing changes", "git log", ""},
		{"conflicting remote tag", "git ls-remote", strings.Repeat("b", 40) + " refs/tags/v2.1.0-alpha.1^{}"},
		{"published metadata", "/releases/tags/", "{"},
		{"published identity", "/releases/tags/", `{"tag_name":"other","draft":false}`},
		{"evidence JSON", "release download", "{"},
		{"evidence source", "release download", `{"source":"other","runs":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBoundaryPublication()
			corrupted := false
			p := publisher{config: Config{Mode: modeAlpha, Source: f.sha}, run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if strings.Contains(name+" "+strings.Join(args, " "), tc.operation) {
					corrupted = true
					return []byte(tc.response), nil
				}
				return f.run(ctx, name, args...)
			}}
			require.Error(t, p.publish(t.Context()))
			assert.True(t, corrupted)
		})
	}
}

func TestReleaseEntryPointsUseValidatedArgumentsAndReportProcessFailures(t *testing.T) {
	dir := t.TempDir()
	falseCommand, err := exec.LookPath("false")
	require.NoError(t, err)
	// Fake executables keep the real process boundary while guaranteeing no
	// network, Git mutation, or release publication can occur.
	for _, name := range []string{"git", "gh"} {
		require.NoError(t, os.Symlink(falseCommand, filepath.Join(dir, name)))
	}
	t.Setenv("PATH", dir)
	require.ErrorContains(t, Run(t.Context(), Config{Mode: modeAlpha, Source: strings.Repeat("a", 40)}), "git:")
	require.ErrorContains(t, Run(t.Context(), Config{Mode: modeAlpha, Source: strings.Repeat("a", 40), Root: filepath.Join(dir, "missing")}), "git:")
	require.ErrorContains(t, Run(t.Context(), Config{Mode: "invalid"}), "invalid release mode")
	require.ErrorContains(t, Dispatch(t.Context(), Config{Mode: modeAlpha, Source: "short"}), "full source SHA")
	assert.ErrorContains(t, Dispatch(t.Context(), Config{Mode: modeAlpha, Source: strings.Repeat("a", 40)}), "dispatch release")
}

func newBoundaryPublication() *fakePublication {
	return &fakePublication{tags: map[string]string{}, remoteTags: map[string]string{}, releases: map[string]releaseMetadata{}, evidence: map[string][]byte{}, sha: strings.Repeat("a", 40)}
}
