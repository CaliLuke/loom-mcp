package framework

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
)

// sdkServerPatchFS contains application-owned Go sources used to replace the
// generated JSON-RPC command with the SDK-backed integration-test command.
// Keeping these as ordinary checked-in sources makes them reviewable, reusable,
// and independently format-checkable.
//
//go:embed testdata/sdk_server_patch/http.go testdata/sdk_server_patch/main.go
var sdkServerPatchFS embed.FS

// findExampleRoot locates the example directory.
func findExampleRoot() string {
	wd, _ := os.Getwd()
	for up := 0; up < 8; up++ {
		root := wd
		for i := 0; i < up; i++ {
			root = filepath.Dir(root)
		}
		// Use integration test fixture module exclusively.
		fixtureRoot := filepath.Join(root, "integration_tests", "fixtures", "assistant")
		if st, err := os.Stat(fixtureRoot); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(fixtureRoot, "go.mod")); err == nil {
				return fixtureRoot
			}
		}
	}
	return ""
}

func cloneExampleRoot(exampleRoot string) (string, error) {
	tmpBase := filepath.Join(filepath.Dir(filepath.Dir(exampleRoot)), ".tmp")
	if err := os.MkdirAll(tmpBase, 0o750); err != nil {
		return "", fmt.Errorf("create temp example base: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(tmpBase, "loom-mcp-example-*")
	if err != nil {
		return "", fmt.Errorf("create temp example root: %w", err)
	}
	walkErr := filepath.WalkDir(exampleRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(exampleRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(tmpRoot, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		//nolint:gosec // Path comes from walking a trusted fixture tree.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode()) //nolint:gosec // Test helper copies trusted fixture files.
	})
	if walkErr != nil {
		_ = os.RemoveAll(tmpRoot)
		return "", fmt.Errorf("clone example root: %w", walkErr)
	}
	return tmpRoot, nil
}

func applySDKServerFixturePatch(exampleRoot string) error {
	cmdDir, err := findServerCmdDir(exampleRoot)
	if err != nil {
		return fmt.Errorf("resolve SDK fixture command dir: %w", err)
	}
	if err := os.MkdirAll(cmdDir, 0o750); err != nil {
		return fmt.Errorf("create SDK fixture command dir: %w", err)
	}
	for _, name := range []string{"http.go", "main.go"} {
		content, err := sdkServerPatchFS.ReadFile(filepath.Join("testdata", "sdk_server_patch", name))
		if err != nil {
			return fmt.Errorf("read checked-in SDK fixture %s: %w", name, err)
		}
		target := filepath.Join(cmdDir, name)
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return fmt.Errorf("write SDK fixture %s: %w", name, err)
		}
	}
	jsonrpcPath := filepath.Join(cmdDir, "jsonrpc.go")
	if err := os.Remove(jsonrpcPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove generated JSON-RPC fixture %s: %w", jsonrpcPath, err)
	}
	return nil
}

// findServerCmdDir finds the server command directory.
func findServerCmdDir(exampleRoot string) (string, error) {
	cmdRoot := filepath.Join(exampleRoot, "cmd")
	entries, err := os.ReadDir(cmdRoot)
	if err != nil {
		return "", fmt.Errorf("read cmd root: %w", err)
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(cmdRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
			candidates = append(candidates, dir)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no server cmd dirs found under %s", cmdRoot)
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "http.go")); err == nil {
			return dir, nil
		}
	}
	return candidates[0], nil
}

// regenerateExample regenerates the example code.
func regenerateExample(t *testing.T, exampleRoot string) error {
	t.Helper()

	root, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()
	if err := cleanGeneratedExampleArtifacts(exampleRoot); err != nil {
		return err
	}
	tidyCmd := tempModuleGoCommand(context.Background(), "mod", "tidy")
	tidyCmd.Dir = exampleRoot
	if out, err := tidyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w\n%s", err, string(out))
	}
	genCmd := tempModuleGoCommand(
		context.Background(),
		"run",
		"-C",
		exampleRoot,
		upstreampaths.LoomCLIPackage,
		"gen",
		"example.com/assistant/design",
	) // #nosec G204
	if out, err := genCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loom gen failed: %w\n%s", err, string(out))
	}
	_ = os.Remove(filepath.Join(exampleRoot, "assistant.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "streaming.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "websocket.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "grpcstream.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "mcp_assistant.go"))
	exCmd := tempModuleGoCommand(
		context.Background(),
		"run",
		"-C",
		exampleRoot,
		upstreampaths.LoomCLIPackage,
		"example",
		"example.com/assistant/design",
	) // #nosec G204
	if out, err := exCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loom example failed: %w\n%s", err, string(out))
	}
	_ = os.Remove(filepath.Join(exampleRoot, "mcp_assistant.go"))
	postTidy := tempModuleGoCommand(context.Background(), "mod", "tidy")
	postTidy.Dir = exampleRoot
	if out, err := postTidy.CombinedOutput(); err != nil {
		return fmt.Errorf("post loom example tidy failed: %w\n%s", err, string(out))
	}
	return nil
}

func ensurePreparedExampleRoot(t *testing.T, exampleRoot string) (string, error) {
	t.Helper()
	codegenMu.Lock()
	defer codegenMu.Unlock()
	if cached, ok := preparedExampleCache[exampleRoot]; ok {
		return cached.root, cached.err
	}
	preparedRoot, err := cloneExampleRoot(exampleRoot)
	if err == nil {
		err = regenerateExample(t, preparedRoot)
	}
	if err == nil {
		err = restoreFixtureCommandTree(exampleRoot, preparedRoot)
	}
	if err != nil {
		_ = os.RemoveAll(preparedRoot)
		preparedExampleCache[exampleRoot] = preparedExample{err: err}
		return "", err
	}
	preparedExampleCache[exampleRoot] = preparedExample{root: preparedRoot}
	return preparedRoot, nil
}

// CleanupTestArtifacts removes process-scoped prepared fixture roots and
// compiled server binaries, then resets both caches. Test binaries that use the
// framework must call it from TestMain after m.Run so cache reuse remains
// available throughout the suite without leaking temporary artifacts.
func CleanupTestArtifacts() error {
	codegenMu.Lock()
	prepared := preparedExampleCache
	preparedExampleCache = map[string]preparedExample{}
	codegenMu.Unlock()

	serverBinMu.Lock()
	binaries := serverBinCache
	serverBinCache = map[string]serverBinaryBuild{}
	serverBinMu.Unlock()

	var cleanupErr error
	for _, cached := range prepared {
		if cached.root == "" {
			continue
		}
		if err := os.RemoveAll(cached.root); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove prepared fixture root %q: %w", cached.root, err))
		}
	}
	for _, cached := range binaries {
		if cached.path == "" {
			continue
		}
		if err := os.Remove(cached.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove cached server binary %q: %w", cached.path, err))
		}
	}
	return cleanupErr
}

// cleanGeneratedExampleArtifacts removes generated example artifacts that can interfere
// with repeated Loom generation in tests.
func cleanGeneratedExampleArtifacts(exampleRoot string) error {
	root, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()
	if err := root.RemoveAll("cmd"); err != nil {
		return fmt.Errorf("remove cmd directory: %w", err)
	}
	const generatedHeader = "Code generated by Loom"
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := root.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(content, []byte(generatedHeader)) {
			if err := root.Remove(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("clean generated example artifacts: %w", err)
	}
	return nil
}

func restoreFixtureCommandTree(fixtureRoot string, exampleRoot string) error {
	sourceRoot := filepath.Join(fixtureRoot, "cmd")
	targetRoot := filepath.Join(exampleRoot, "cmd")
	if err := os.RemoveAll(targetRoot); err != nil {
		return fmt.Errorf("remove regenerated cmd directory: %w", err)
	}
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return fmt.Errorf("open fixture cmd root: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()
	targetFS, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = targetFS.Close()
	}()
	return filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		targetRel := filepath.Join("cmd", rel)
		targetPath := filepath.Join(targetRoot, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		data, err := source.ReadFile(rel)
		if err != nil {
			return err
		}
		if err := targetFS.MkdirAll(filepath.Dir(targetRel), 0o750); err != nil {
			return err
		}
		return targetFS.WriteFile(targetRel, data, info.Mode())
	})
}

// buildServerBinary compiles the server binary once for fast parallel test starts.
func buildServerBinary(exampleRoot string) (string, error) {
	serverBinMu.Lock()
	defer serverBinMu.Unlock()

	cmdPath, err := findServerCmdDir(exampleRoot)
	if err != nil {
		return "", err
	}
	cacheKey, err := serverBinaryCacheKey(exampleRoot, cmdPath)
	if err != nil {
		return "", err
	}
	if cached, ok := serverBinCache[cacheKey]; ok {
		return cached.path, cached.err
	}

	tmpFile, err := os.CreateTemp("", "mcp-test-server-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for binary: %w", err)
	}
	binPath := filepath.Clean(tmpFile.Name())
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close temp file for binary: %w", err)
	}

	buildCmd := tempModuleGoCommand(context.Background(), "build", "-o", binPath, ".") // #nosec G204 -- cmdPath is resolved from the trusted fixture tree
	buildCmd.Dir = cmdPath
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		buildErr := fmt.Errorf("go build failed in %s: %w\n%s", cmdPath, err, string(out))
		if removeErr := os.Remove(binPath); removeErr != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("remove temp binary failed: %w", removeErr))
		}
		serverBinCache[cacheKey] = serverBinaryBuild{err: buildErr}
		return "", buildErr
	}
	if _, err := os.Stat(binPath); err != nil {
		buildErr := fmt.Errorf("binary not found after build: %w", err)
		serverBinCache[cacheKey] = serverBinaryBuild{err: buildErr}
		return "", buildErr
	}
	serverBinCache[cacheKey] = serverBinaryBuild{path: binPath}
	return binPath, nil
}

func tempModuleGoCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", args...) // #nosec G204 -- test harness executes fixed Go toolchain commands.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	return cmd
}

func serverBinaryCacheKey(exampleRoot, cmdPath string) (string, error) {
	rel, err := filepath.Rel(exampleRoot, cmdPath)
	if err != nil {
		return "", fmt.Errorf("rel server cmd dir: %w", err)
	}
	return rel, nil
}
