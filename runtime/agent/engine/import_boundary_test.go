package engine_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporalImportBoundary(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "third_party" || entry.Name() == "node_modules" || filepath.ToSlash(relative) == "integration_tests/fixtures" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "runtime/agent/engine/temporal/") || strings.HasPrefix(relative, "runtime/temporaltrace/") {
			return nil
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
			assert.False(t, strings.HasPrefix(importPath, "go.temporal.io/"), "%s imports %s", relative, importPath)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestSelvedgeImportBoundary(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "third_party" || entry.Name() == "node_modules" || filepath.ToSlash(relative) == "integration_tests/fixtures" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "runtime/agent/engine/selvedge/") {
			return nil
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
			assert.False(t, importPath == "github.com/CaliLuke/selvedge" || strings.HasPrefix(importPath, "github.com/CaliLuke/selvedge/"), "%s imports %s", relative, importPath)
		}
		return nil
	})
	require.NoError(t, err)
}
