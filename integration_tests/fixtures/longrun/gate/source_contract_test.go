package suite_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("CGO_ENABLED", "0")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	files := map[string]string{
		"go.mod":                  "module example.com/root\n\ngo 1.27.0\n",
		"go.sum":                  "",
		"go.work":                 "go 1.27.0\nuse (\n.\n./integration_tests/fixtures/longrun\n)\n",
		"go.work.sum":             "",
		"library/library.go":      "package library\nimport _ \"embed\"\n//go:embed prompt.tmpl\nvar Prompt string\n",
		"library/prompt.tmpl":     "original prompt",
		"library/library_test.go": "package library\n",
		"unrelated/unrelated.go":  "package unrelated\n",
		"integration_tests/fixtures/longrun/go.mod":                      "module example.com/fixture\n\ngo 1.27.0\n",
		"integration_tests/fixtures/longrun/go.sum":                      "",
		"integration_tests/fixtures/longrun/cmd/compaction-gate/main.go": "package main\nimport \"example.com/root/library\"\nfunc main(){println(library.Prompt)}\n",
		"integration_tests/fixtures/longrun/design/design.go":            "package design\nimport _ \"embed\"\n//go:embed template.tpl\nvar Template string\n",
		"integration_tests/fixtures/longrun/design/template.tpl":         "template",
	}
	for path, data := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(data), 0600))
	}
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	return root
}

func sourcePaths(in suite.SourceIdentity) []string {
	out := make([]string, 0, len(in.Owners))
	for _, owner := range in.Owners {
		out = append(out, owner.Path)
	}
	return out
}

func TestK19cContractSourceClosureAndAssets(t *testing.T) {
	root := sourceFixture(t)
	identity, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.NotEmpty(t, identity.SHA256)
	paths := sourcePaths(identity)
	for _, path := range []string{"go.mod", "go.sum", "go.work", "library/library.go", "library/prompt.tmpl", "integration_tests/fixtures/longrun/go.mod", "integration_tests/fixtures/longrun/go.sum", "integration_tests/fixtures/longrun/design/template.tpl"} {
		assert.Contains(t, paths, path)
	}
	assert.NotContains(t, paths, "go.work.sum")
	assert.NotContains(t, paths, "library/library_test.go")
	assert.NotContains(t, paths, "unrelated/unrelated.go")
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/prompt.tmpl"), []byte("changed prompt"), 0600))
	changed, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.NotEqual(t, identity.SHA256, changed.SHA256)
}

func TestK19cContractSourceRelocationAndExclusions(t *testing.T) {
	root := sourceFixture(t)
	identity, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.work.sum"), []byte("example.com/unused v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/library_test.go"), []byte("package library\n// test-only change\n"), 0600))
	changed, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, identity.SHA256, changed.SHA256)
	relocated := t.TempDir()
	require.NoError(t, os.CopyFS(relocated, os.DirFS(root)))
	t.Setenv("GOWORK", filepath.Join(relocated, "go.work"))
	same, err := suite.SourceSnapshot(t.Context(), relocated)
	require.NoError(t, err)
	assert.Equal(t, identity.SHA256, same.SHA256)
}

func TestK19cContractSourceExecutableRejectsStaleBinary(t *testing.T) {
	root := sourceFixture(t)
	main := `package main
import("fmt";"os";"os/exec";"strings";"example.com/root/library")
func main(){
 if len(os.Args)==2 && os.Args[1]=="_source-executable" {
  path,err:=os.Executable();if err!=nil{panic(err)}
  data,err:=exec.Command("go","tool","buildid",path).Output();if err!=nil{panic(err)}
  fmt.Println(strings.TrimSpace(string(data)));return
 }
 if len(os.Args)==3 && os.Args[1]=="capture" {
  path,err:=os.Executable();if err!=nil{panic(err)}
  data,err:=os.ReadFile(path);if err!=nil{panic(err)}
  if err:=os.WriteFile(os.Args[2],data,0700);err!=nil{panic(err)}
  return
 }
 println(library.Prompt)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "integration_tests/fixtures/longrun/cmd/compaction-gate/main.go"), []byte(main), 0600))
	executable := filepath.Join(t.TempDir(), "compaction-gate")
	t.Setenv("CGO_ENABLED", "1")
	sourceBuild(t, root, executable)
	t.Setenv("CGO_ENABLED", "0")
	require.ErrorContains(t, suite.VerifySourceExecutable(t.Context(), root, executable), "CGO_ENABLED=0")
	sourceBuild(t, root, executable)
	require.NoError(t, suite.VerifySourceExecutable(t.Context(), root, executable))
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/prompt.tmpl"), []byte("new executable behavior"), 0600))
	require.Error(t, suite.VerifySourceExecutable(t.Context(), root, executable))
	sourceBuild(t, root, executable)
	require.NoError(t, suite.VerifySourceExecutable(t.Context(), root, executable))
	command := exec.CommandContext(t.Context(), "go", "run", "./cmd/compaction-gate", "capture")
	command.Args = append(command.Args, executable)
	command.Dir = filepath.Join(root, "integration_tests/fixtures/longrun")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, suite.VerifySourceExecutable(t.Context(), root, executable))
	before, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	sourceCommitFixture(t, root)
	after, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, before.SHA256, after.SHA256)
	sourceBuild(t, root, executable)
	require.NoError(t, suite.VerifySourceExecutable(t.Context(), root, executable))
}

func sourceBuild(t *testing.T, root, executable string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "build")
	command.Args = append(command.Args, "-o", executable, "./cmd/compaction-gate")
	command.Dir = filepath.Join(root, "integration_tests/fixtures/longrun")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func TestK19cContractSourceActualClosure(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	root, err := filepath.Abs("../../../..")
	require.NoError(t, err)
	confined, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, confined.Close()) })
	original, err := confined.ReadFile("go.work.sum")
	require.NoError(t, err)
	t.Cleanup(func() {
		current, err := confined.ReadFile("go.work.sum")
		require.NoError(t, err)
		assert.Equal(t, sha256.Sum256(original), sha256.Sum256(current), "source resolution must not modify auxiliary workspace checksums")
	})
	identity, err := suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	paths := sourcePaths(identity)
	for _, path := range []string{
		"go.work", "go.mod", "go.sum",
		"integration_tests/fixtures/agent_features/go.mod", "integration_tests/fixtures/agent_features/go.sum",
		"integration_tests/fixtures/assistant/go.mod", "integration_tests/fixtures/assistant/go.sum",
		"integration_tests/fixtures/longrun/go.mod", "integration_tests/fixtures/longrun/go.sum",
		"runtime/agent/runtime/summary_prompt.tmpl", "trajectory/schema/trajectory.v1.schema.json",
		"integration_tests/fixtures/longrun/gate/source.go",
	} {
		assert.Contains(t, paths, path)
	}
	assert.NotContains(t, paths, "go.work.sum")
	assert.True(t, slices.ContainsFunc(paths, func(path string) bool { return strings.HasPrefix(path, "codegen/agent/templates/") }))
	assert.True(t, slices.ContainsFunc(paths, func(path string) bool { return strings.HasPrefix(path, "codegen/mcp/templates/") }))
}

func TestK19cContractSourceBundleRecovery(t *testing.T) {
	root := sourceFixture(t)
	base := sourceCommitFixture(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/prompt.tmpl"), []byte("changed prompt"), 0600))
	manifest, bundle, err := suite.BuildSourceManifest(t.Context(), root, "offline", base)
	require.NoError(t, err)
	require.NotEmpty(t, manifest.BundleSHA256)
	assert.Contains(t, string(bundle), "library/prompt.tmpl")
	assert.NotContains(t, manifest.BundlePaths, "go.work.sum")
	require.NoError(t, suite.VerifySourceManifest(t.Context(), root, manifest, bundle))
	corrupted := append(append([]byte(nil), bundle...), []byte("corruption")...)
	require.Error(t, suite.VerifySourceManifest(t.Context(), root, manifest, corrupted))
	manifest.Identity.Owners[0].SHA256 = strings.Repeat("0", 64)
	assert.Error(t, suite.VerifySourceManifest(t.Context(), root, manifest, bundle))
}

func TestK19cContractSourceBundleDeletionExcludesTestsAndEvidence(t *testing.T) {
	root := sourceFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/README.md"), []byte("documentation"), 0600))
	base := sourceCommitFixture(t, root)
	// Remove the entire formerly selected package. A current-directory heuristic
	// cannot see its deleted owners, but the retained compiler closure can.
	require.NoError(t, os.WriteFile(filepath.Join(root, "integration_tests/fixtures/longrun/cmd/compaction-gate/main.go"), []byte("package main\nfunc main() {}\n"), 0600))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "library")))
	manifest, bundle, err := suite.BuildSourceManifest(t.Context(), root, "offline", base)
	require.NoError(t, err)
	assert.Contains(t, string(bundle), "library/library.go")
	assert.Contains(t, string(bundle), "library/prompt.tmpl")
	assert.NotContains(t, string(bundle), "library_test.go")
	assert.NotContains(t, string(bundle), "README.md")
	assert.NotContains(t, string(bundle), "go.work.sum")
	require.NoError(t, suite.VerifySourceManifest(t.Context(), root, manifest, bundle))
	// Moving the exact owners and their retained Git objects must preserve proof.
	relocated, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.CopyFS(relocated, os.DirFS(root)))
	t.Setenv("GOWORK", filepath.Join(relocated, "go.work"))
	assert.NoError(t, suite.VerifySourceManifest(t.Context(), relocated, manifest, bundle))
}

// Populate a disposable Git object database without touching any Git index.
func sourceCommitFixture(t *testing.T, root string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", "init", "--quiet")
	command.Dir = root
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	var stream bytes.Buffer
	confined, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, confined.Close()) })
	stream.WriteString("commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 1 +0000\ndata 7\nfixture\n")
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := confined.ReadFile(relative)
		if err != nil {
			return err
		}
		fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n", filepath.ToSlash(relative), len(data))
		stream.Write(data)
		stream.WriteByte('\n')
		return nil
	}))
	stream.WriteByte('\n')
	command = exec.CommandContext(t.Context(), "git", "fast-import", "--quiet")
	command.Dir = root
	command.Stdin = &stream
	output, err = command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	command = exec.CommandContext(t.Context(), "git", "symbolic-ref", "HEAD", "refs/heads/main")
	command.Dir = root
	output, err = command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	command = exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	command.Dir = root
	output, err = command.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}

func TestK19cContractSourceRejectsModifiedSelectedModule(t *testing.T) {
	root := sourceFixture(t)
	proxy := t.TempDir()
	cache := t.TempDir()
	t.Cleanup(func() {
		confined, err := os.OpenRoot(cache)
		require.NoError(t, err)
		walkErr := filepath.WalkDir(cache, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(cache, path)
			if err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if entry.IsDir() {
				mode |= 0100
			}
			return confined.Chmod(relative, mode)
		})
		require.NoError(t, errors.Join(walkErr, confined.Close()))
	})
	t.Setenv("GOPROXY", "file://"+proxy)
	t.Setenv("GONOSUMDB", "example.com/*")
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOTOOLCHAIN", "local")
	prefix := filepath.Join(proxy, "example.com/remote/@v")
	require.NoError(t, os.MkdirAll(prefix, 0700))
	mod := "module example.com/remote\n\ngo 1.27.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "v1.0.0.mod"), []byte(mod), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "v1.0.0.info"), []byte(`{"Version":"v1.0.0","Time":"2020-01-01T00:00:00Z"}`), 0600))
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for path, data := range map[string]string{"go.mod": mod, "remote.go": "package remote\nconst Value = 1\n"} {
		file, err := writer.Create("example.com/remote@v1.0.0/" + path)
		require.NoError(t, err)
		_, err = io.WriteString(file, data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "v1.0.0.zip"), archive.Bytes(), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n\ngo 1.27.0\nrequire example.com/remote v1.0.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "library/library.go"), []byte("package library\nimport (_ \"embed\"; _ \"example.com/remote\")\n//go:embed prompt.tmpl\nvar Prompt string\n"), 0600))
	command := exec.CommandContext(t.Context(), "go", "mod", "download", "example.com/remote")
	command.Dir = root
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	_, err = suite.SourceSnapshot(t.Context(), root)
	require.NoError(t, err)
	cached := filepath.Join(cache, "example.com/remote@v1.0.0/remote.go")
	require.NoError(t, os.Chmod(cached, 0600))
	require.NoError(t, os.WriteFile(cached, []byte("package remote\nconst Value = 2\n"), 0600))
	confined, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, confined.Close()) })
	original, err := confined.ReadFile("go.work.sum")
	require.NoError(t, err)
	_, err = suite.SourceSnapshot(t.Context(), root)
	require.Error(t, err, "selected dependency contents must agree with their recorded checksum")
	current, err := confined.ReadFile("go.work.sum")
	require.NoError(t, err)
	assert.Equal(t, original, current, "failed provenance collection must be read-only")
}
