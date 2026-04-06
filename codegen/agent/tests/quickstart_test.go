package tests

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
)

var goaCoreReplacePattern = regexp.MustCompile(`(?m)^replace goa\.design/goa/v3 => .+$`)
var loomCoreReplacePattern = regexp.MustCompile(`(?m)^replace github\.com/CaliLuke/loom => .+$`)

// TestQuickstartGeneratesAndRuns verifies that the quickstart example:
// 1. Successfully generates code with `loom gen`
// 2. Successfully generates example with `loom example`
// 3. Compiles without errors
// 4. Starts successfully or exits successfully as a one-shot example
//
// This test ensures the quickstart doesn't break as the codebase evolves.
func TestQuickstartGeneratesAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping quickstart integration test in short mode")
	}

	repoRoot := testRepoRoot()
	quickstartSrcDir := filepath.Join(repoRoot, "quickstart")

	// Check required preconditions
	designPath := filepath.Join(quickstartSrcDir, "design", "design.go")
	if _, err := os.Stat(designPath); os.IsNotExist(err) {
		t.Skipf("quickstart design not found at %s, skipping integration test", designPath)
	}
	goModPath := filepath.Join(quickstartSrcDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		t.Skipf("quickstart go.mod not found at %s, skipping integration test", goModPath)
	}

	// Copy quickstart into a temp workspace so tests never mutate the repo tree.
	quickstartDir := filepath.Join(t.TempDir(), "quickstart")
	if err := copyDir(quickstartSrcDir, quickstartDir); err != nil {
		t.Fatalf("copy quickstart fixture: %v", err)
	}

	// The quickstart module uses a relative replace for loom-mcp (=> ..) so it can
	// be generated and run from the repo tree. Once copied into a temp dir, that
	// relative path no longer points at the repo root. Rewrite it to an absolute
	// replace so `loom gen` and `go mod tidy` can resolve the local loom-mcp module.
	if err := rewriteQuickstartGoMod(repoRoot, quickstartDir); err != nil {
		t.Fatalf("rewrite quickstart go.mod: %v", err)
	}

	// Ensure we have a clean state (remove generated files that aren't committed)
	// Note: We don't remove the design/ directory which should be committed
	for _, dir := range []string{"gen", "cmd", "internal"} {
		path := filepath.Join(quickstartDir, dir)
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
			t.Logf("warning: could not clean %s: %v", dir, err)
		}
	}

	// Remove any user-created files that depend on generated code to allow clean bootstrap
	for _, file := range []string{"orchestrator.go"} {
		path := filepath.Join(quickstartDir, file)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Logf("warning: could not remove %s: %v", file, err)
		}
	}

	ctx := context.Background()

	// Step 0: Ensure the module graph is tidy before running loom. The loom CLI
	// compiles the design package via `go list`, which fails when the module has
	// pending sum updates.
	t.Run("go_mod_tidy_pre", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "mod", "tidy")
	})

	// Step 1: Run loom gen
	t.Run("goa_gen", func(t *testing.T) {
		out := runCommand(t, ctx, quickstartDir, "run", upstreampaths.LoomCLIPackage, "gen", "example.com/quickstart/design")
		t.Logf("loom gen output:\n%s", out)
	})

	// Step 2: Run loom example
	t.Run("goa_example", func(t *testing.T) {
		out := runCommand(t, ctx, quickstartDir, "run", upstreampaths.LoomCLIPackage, "example", "example.com/quickstart/design")
		t.Logf("loom example output:\n%s", out)
	})

	// Step 2b: Ensure module sums include dependencies pulled in by generated code.
	// This is required when tests run with module updates disabled (e.g. GOFLAGS=-mod=readonly).
	t.Run("go_mod_tidy", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "mod", "tidy")
	})

	// Step 3: Verify compilation
	t.Run("go_build", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "build", "./cmd/...")
	})

	// Step 4: Build and run the generated binary so the test exercises the
	// scaffold directly rather than the `go run` wrapper. Some quickstarts are
	// one-shot examples, others are long-running service stubs, so this step
	// accepts either a clean exit or a process that stays up until the test
	// stops it.
	t.Run("run_example", func(t *testing.T) {
		binaryPath := filepath.Join(t.TempDir(), "orchestrator")
		runCommand(t, ctx, quickstartDir, "build", "-o", binaryPath, "./cmd/orchestrator")
		out := runStartableCommand(t, quickstartDir, binaryPath)
		t.Logf("Example output:\n%s", out)
	})
}

// TestQuickstartDesignExists verifies the design file is present and parseable.
func TestQuickstartDesignExists(t *testing.T) {
	repoRoot := testRepoRoot()
	designPath := filepath.Join(repoRoot, "quickstart", "design", "design.go")
	if _, err := os.Stat(designPath); os.IsNotExist(err) {
		t.Fatalf("design file not found at %s", designPath)
	}
}

func testRepoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		//nolint:gosec // Test helper copies trusted fixture files.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		//nolint:gosec // Test helper copies trusted fixture files into a temp workspace.
		return os.WriteFile(target, data, info.Mode())
	})
}

func rewriteQuickstartGoMod(repoRoot string, quickstartDir string) error {
	modPath := filepath.Join(quickstartDir, "go.mod")
	//nolint:gosec // Test helper reads a trusted fixture file.
	raw, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	updated := strings.ReplaceAll(string(raw), "replace github.com/CaliLuke/loom-mcp => ..", "replace github.com/CaliLuke/loom-mcp => "+repoRoot)

	rootModPath := filepath.Join(repoRoot, "go.mod")
	//nolint:gosec // Test helper reads the local repo module file.
	rootRaw, err := os.ReadFile(rootModPath)
	if err != nil {
		return err
	}
	rootReplace := goaCoreReplacePattern.FindString(string(rootRaw))
	if rootReplace != "" {
		if goaCoreReplacePattern.MatchString(updated) {
			updated = goaCoreReplacePattern.ReplaceAllString(updated, rootReplace)
		} else {
			updated = strings.TrimRight(updated, "\n") + "\n" + rootReplace + "\n"
		}
	}
	loomReplace := loomCoreReplacePattern.FindString(string(rootRaw))
	if loomReplace != "" {
		if loomCoreReplacePattern.MatchString(updated) {
			updated = loomCoreReplacePattern.ReplaceAllString(updated, loomReplace)
		} else {
			updated = strings.TrimRight(updated, "\n") + "\n" + loomReplace + "\n"
		}
	}

	//nolint:gosec // Test helper rewrites a trusted copied fixture file inside t.TempDir().
	return os.WriteFile(modPath, []byte(updated), 0o600)
}

func runCommand(t *testing.T, ctx context.Context, dir string, args ...string) []byte {
	t.Helper()

	//nolint:gosec // Test helper executes repo-controlled tooling commands.
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\nOutput:\n%s", strings.Join(append([]string{"go"}, args...), " "), err, out)
	}
	return out
}

func runStartableCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	//nolint:gosec // Test helper executes a binary built in the temp fixture workspace.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe failed: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s start failed: %v", strings.Join(append([]string{name}, args...), " "), err)
	}

	var output bytes.Buffer
	var outputMu sync.Mutex
	copyDone := make(chan struct{}, 2)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, stdout)
		outputMu.Lock()
		output.Write(buf.Bytes())
		outputMu.Unlock()
		copyDone <- struct{}{}
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, stderr)
		outputMu.Lock()
		output.Write(buf.Bytes())
		outputMu.Unlock()
		copyDone <- struct{}{}
	}()

	waitc := make(chan error, 1)
	go func() {
		waitc <- cmd.Wait()
	}()

	select {
	case err := <-waitc:
		<-copyDone
		<-copyDone
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("%s timed out\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), output.String())
		}
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("%s failed: %v\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), err, output.String())
			}
			t.Fatalf("%s exited with error\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), output.String())
		}
		return output.String()
	case <-time.After(750 * time.Millisecond):
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("failed to stop example process: %v", err)
		}
		err := <-waitc
		<-copyDone
		<-copyDone
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("%s timed out\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), output.String())
		}
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("%s failed while stopping: %v\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), err, output.String())
			}
		}
		return output.String()
	}
}
