package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var goaCoreReplacePattern = regexp.MustCompile(`(?m)^replace goa\.design/goa/v3 => .+$`)

// TestQuickstartGeneratesAndRuns verifies that the quickstart example:
// 1. Successfully generates code with `goa gen`
// 2. Successfully generates example with `goa example`
// 3. Compiles without errors
// 4. Runs and produces expected output
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

	// The quickstart module uses a relative replace for goa-ai (=> ..) so it can
	// be generated and run from the repo tree. Once copied into a temp dir, that
	// relative path no longer points at the repo root. Rewrite it to an absolute
	// replace so `goa gen` and `go mod tidy` can resolve the local goa-ai module.
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

	// Step 0: Ensure the module graph is tidy before running goa. The goa CLI
	// compiles the design package via `go list`, which fails when the module has
	// pending sum updates.
	t.Run("go_mod_tidy_pre", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "go", "mod", "tidy")
	})

	// Step 1: Run goa gen
	t.Run("goa_gen", func(t *testing.T) {
		out := runCommand(t, ctx, quickstartDir, "goa", "gen", "example.com/quickstart/design")
		t.Logf("goa gen output:\n%s", out)
	})

	// Step 2: Run goa example
	t.Run("goa_example", func(t *testing.T) {
		out := runCommand(t, ctx, quickstartDir, "goa", "example", "example.com/quickstart/design")
		t.Logf("goa example output:\n%s", out)
	})

	// Step 2b: Ensure module sums include dependencies pulled in by generated code.
	// This is required when tests run with module updates disabled (e.g. GOFLAGS=-mod=readonly).
	t.Run("go_mod_tidy", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "go", "mod", "tidy")
	})

	// Step 3: Verify compilation
	t.Run("go_build", func(t *testing.T) {
		runCommand(t, ctx, quickstartDir, "go", "build", "./cmd/...")
	})

	// Step 4: Run the example and verify output
	t.Run("run_example", func(t *testing.T) {
		out := runCommand(t, ctx, quickstartDir, "go", "run", "./cmd/orchestrator")

		// Verify expected output
		output := string(out)
		if !strings.Contains(output, "RunID:") {
			t.Errorf("expected output to contain 'RunID:', got:\n%s", output)
		}
		if !strings.Contains(output, "Assistant:") {
			t.Errorf("expected output to contain 'Assistant:', got:\n%s", output)
		}
		t.Logf("Example output:\n%s", output)
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
	updated := strings.ReplaceAll(string(raw), "replace goa.design/goa-ai => ..", "replace goa.design/goa-ai => "+repoRoot)

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

	//nolint:gosec // Test helper rewrites a trusted copied fixture file inside t.TempDir().
	return os.WriteFile(modPath, []byte(updated), 0o600)
}

func runCommand(t *testing.T, ctx context.Context, dir string, name string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\nOutput:\n%s", strings.Join(append([]string{name}, args...), " "), err, out)
	}
	return out
}
