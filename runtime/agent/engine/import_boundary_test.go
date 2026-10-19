package engine_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type importBoundary struct {
	allowedPaths []string
	forbidden    func(string) bool
}

var (
	temporalImportBoundary = importBoundary{
		allowedPaths: []string{"runtime/agent/engine/temporal/", "runtime/temporaltrace/"},
		forbidden: func(importPath string) bool {
			return strings.HasPrefix(importPath, "go.temporal.io/")
		},
	}
	selvedgeImportBoundary = importBoundary{
		allowedPaths: []string{"runtime/agent/engine/selvedge/"},
		forbidden: func(importPath string) bool {
			return importPath == "github.com/CaliLuke/selvedge" || strings.HasPrefix(importPath, "github.com/CaliLuke/selvedge/")
		},
	}
)

func TestTemporalImportBoundary(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	violations, err := temporalImportBoundary.scan(root)
	require.NoError(t, err)
	assert.Empty(t, violations)
}

func TestSelvedgeImportBoundary(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	violations, err := selvedgeImportBoundary.scan(root)
	require.NoError(t, err)
	assert.Empty(t, violations)
}

func TestImportBoundarySourceTree(t *testing.T) {
	for _, boundary := range []struct {
		name       string
		rule       importBoundary
		importPath string
	}{
		{"temporal", temporalImportBoundary, "go.temporal.io/api/common/v1"},
		{"selvedge", selvedgeImportBoundary, "github.com/CaliLuke/selvedge"},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			for _, location := range []struct {
				name string
				path string
			}{
				{"cache", ".cache/snapshot/runtime/agent/engine/" + boundary.name + "/source.go"},
				{"vendor", "vendor/example.com/dependency/source.go"},
			} {
				t.Run(location.name, func(t *testing.T) {
					root := t.TempDir()
					writeImportBoundarySource(t, root, location.path, boundary.importPath)
					violations, err := boundary.rule.scan(root)
					require.NoError(t, err)
					assert.Empty(t, violations)

					// This source is newly created and is not tracked by Git.
					const realSource = "runtime/agent/new_package/source.go"
					writeImportBoundarySource(t, root, realSource, boundary.importPath)
					violations, err = boundary.rule.scan(root)
					require.NoError(t, err)
					assert.Equal(t, []string{realSource + " imports " + boundary.importPath}, violations)
				})
			}
			for _, allowed := range boundary.rule.allowedPaths {
				t.Run(allowed, func(t *testing.T) {
					root := t.TempDir()
					writeImportBoundarySource(t, root, allowed+"source.go", boundary.importPath)
					violations, err := boundary.rule.scan(root)
					require.NoError(t, err)
					assert.Empty(t, violations)
				})
			}
		})
	}
}

func writeImportBoundarySource(t *testing.T, root, relative, importPath string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	source := fmt.Sprintf("package fixture\nimport _ %q\n", importPath)
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
}

// scan checks current source files, including files not tracked by Git.
func (boundary importBoundary) scan(root string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".cache", "third_party", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if filepath.ToSlash(relative) == "integration_tests/fixtures" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative = filepath.ToSlash(relative)
		for _, allowed := range boundary.allowedPaths {
			if strings.HasPrefix(relative, allowed) {
				return nil
			}
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range source.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if boundary.forbidden(importPath) {
				violations = append(violations, relative+" imports "+importPath)
			}
		}
		return nil
	})
	return violations, err
}
