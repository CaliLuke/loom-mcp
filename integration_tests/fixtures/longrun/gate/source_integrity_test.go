package suite_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cContractSourceResealedNestedIdentity(t *testing.T) {
	root := sourceFixture(t)
	base := sourceCommitFixture(t, root)
	manifest, bundle, err := suite.BuildSourceManifest(t.Context(), root, "offline", base)
	require.NoError(t, err)
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(*suite.SourceIdentity)
	}{
		{"owners", func(i *suite.SourceIdentity) { i.Owners = nil }},
		{"modules", func(i *suite.SourceIdentity) { i.Modules = nil }},
		{"build", func(i *suite.SourceIdentity) { delete(i.Build, "GOARCH") }},
		{"toolchain", func(i *suite.SourceIdentity) { delete(i.Toolchain, "bin/go") }},
		{"version", func(i *suite.SourceIdentity) { i.Version = 2 }},
		{"stale_digest", func(i *suite.SourceIdentity) { i.Owners[0].SHA256 = gate.Digest([]byte("changed")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var malformed suite.SourceManifest
			require.NoError(t, json.Unmarshal(encoded, &malformed))
			tc.mutate(&malformed.Identity)
			malformed.SHA256 = ""
			data, err := json.Marshal(malformed)
			require.NoError(t, err)
			malformed.SHA256 = gate.Digest(data)
			assert.Error(t, suite.VerifySourceManifest(t.Context(), root, malformed, bundle))
		})
	}
}

func TestK19cContractSourceRejectsNativeMode(t *testing.T) {
	root := sourceFixture(t)
	t.Setenv("CGO_ENABLED", "1")
	_, err := suite.SourceSnapshot(t.Context(), root)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CGO_ENABLED=0")
}

func TestK19cContractSourceSharedAssemblerHeaders(t *testing.T) {
	root := sourceFixture(t)
	identity, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.NotEmpty(t, identity.Toolchain["pkg/include/textflag.h"])
	assert.NotEmpty(t, identity.Toolchain["pkg/include/funcdata.h"])
}

func TestK19cContractSourceSharedHeaderDrift(t *testing.T) {
	root := sourceFixture(t)
	toolchain, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	// Copy the toolchain into a private fixture before mutating shared headers.
	require.NoError(t, os.CopyFS(toolchain, os.DirFS(runtime.GOROOT())))
	t.Setenv("GOROOT", toolchain)
	before, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	header := filepath.Join(toolchain, "pkg/include/textflag.h")
	data, err := os.ReadFile(header)
	require.NoError(t, err)
	confined, err := os.OpenRoot(toolchain)
	require.NoError(t, err)
	require.NoError(t, confined.WriteFile("pkg/include/textflag.h", append(data, []byte("\n// private fixture header drift\n")...), 0600))
	require.NoError(t, confined.Close())
	after, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.NotEqual(t, before.SHA256, after.SHA256)
}
